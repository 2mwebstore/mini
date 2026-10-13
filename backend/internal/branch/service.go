// Package branch manages company branches and their branch admins. The owner
// (super admin) creates branches and assigns exactly one admin to each; a branch
// admin may adjust their own branch's office location and Telegram chats.
package branch

import (
	"context"
	"errors"
	"regexp"
	"strings"

	"attendance/backend/internal/apperr"
	"attendance/backend/internal/audit"
	"attendance/backend/internal/auth"
	"attendance/backend/internal/models"

	mysqldrv "github.com/go-sql-driver/mysql"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

const (
	ActionCreate      = "branch.create"
	ActionUpdate      = "branch.update"
	ActionAssignAdmin = "branch.assign_admin"
)

type Service struct{ db *gorm.DB }

func NewService(db *gorm.DB) *Service { return &Service{db: db} }

var codeRe = regexp.MustCompile(`^[A-Za-z0-9_-]{2,32}$`)

// Input is a create/update body; nil fields are left unchanged on update.
type Input struct {
	Code                 *string  `json:"code"`
	Name                 *string  `json:"name"`
	Address              *string  `json:"address"`
	Status               *string  `json:"status"` // ACTIVE | INACTIVE
	OfficeLatitude       *float64 `json:"office_latitude"`
	OfficeLongitude      *float64 `json:"office_longitude"`
	AllowedRadiusMeters  *float64 `json:"allowed_radius_meters"`
	UseCompanyOffice     bool     `json:"use_company_office"` // clears the branch's own office location
	TelegramGroupID      *int64   `json:"telegram_group_id"`
	TelegramReviewChatID *int64   `json:"telegram_review_chat_id"`
}

// List returns the branches the actor may see: all for company-wide staff
// (super/HR admins), otherwise only the actor's own branch.
func (s *Service) List(ctx context.Context, actor *auth.Actor) ([]models.Branch, error) {
	q := s.db.WithContext(ctx).Preload("Admin").Order("name")
	if !actor.Has(auth.PermAllView) {
		if actor.Employee.BranchID == nil {
			return []models.Branch{}, nil
		}
		q = q.Where("id = ?", *actor.Employee.BranchID)
	}
	out := []models.Branch{}
	if err := q.Find(&out).Error; err != nil {
		return nil, err
	}
	return out, s.countStaff(ctx, out)
}

func (s *Service) Get(ctx context.Context, actor *auth.Actor, id uint64) (*models.Branch, error) {
	if !actor.Has(auth.PermAllView) && (actor.Employee.BranchID == nil || *actor.Employee.BranchID != id) {
		return nil, apperr.NotFound("BRANCH_NOT_FOUND", "Branch not found.")
	}
	var b models.Branch
	if err := s.db.WithContext(ctx).Preload("Admin").First(&b, id).Error; err != nil {
		return nil, apperr.NotFound("BRANCH_NOT_FOUND", "Branch not found.")
	}
	list := []models.Branch{b}
	if err := s.countStaff(ctx, list); err != nil {
		return nil, err
	}
	return &list[0], nil
}

func (s *Service) Create(ctx context.Context, actor *auth.Actor, in Input) (*models.Branch, error) {
	if !actor.Has(auth.PermBranchesManage) {
		return nil, apperr.Forbidden("Only the owner can create branches.")
	}
	if in.Code == nil || in.Name == nil {
		return nil, apperr.BadRequest("INVALID_BRANCH", "Code and name are required.")
	}
	b := models.Branch{Status: models.EmployeeActive}
	if err := apply(&b, in); err != nil {
		return nil, err
	}
	err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Omit(clause.Associations).Create(&b).Error; err != nil {
			if isDuplicate(err) {
				return apperr.Conflict("DUPLICATE_BRANCH", "A branch with code "+b.Code+" already exists.")
			}
			return err
		}
		return audit.Write(ctx, tx, actor, audit.Entry{Action: ActionCreate, EntityType: "branch", EntityID: b.ID,
			Details: map[string]any{"code": b.Code, "name": b.Name}})
	})
	if err != nil {
		return nil, err
	}
	return s.Get(ctx, actor, b.ID)
}

// Update: the owner may change anything; a branch admin only their own branch's
// office location and Telegram chats.
func (s *Service) Update(ctx context.Context, actor *auth.Actor, id uint64, in Input) (*models.Branch, error) {
	owner := actor.Has(auth.PermBranchesManage)
	own := actor.Has(auth.PermBranchSettings) && actor.Employee.BranchID != nil && *actor.Employee.BranchID == id
	if !owner && !own {
		return nil, apperr.ErrForbidden
	}
	if !owner && (in.Code != nil || in.Name != nil || in.Address != nil || in.Status != nil) {
		return nil, apperr.Forbidden("Only the owner can rename, move or close a branch.")
	}
	var b models.Branch
	if err := s.db.WithContext(ctx).First(&b, id).Error; err != nil {
		return nil, apperr.NotFound("BRANCH_NOT_FOUND", "Branch not found.")
	}
	before := b
	if err := apply(&b, in); err != nil {
		return nil, err
	}
	err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Omit(clause.Associations).Save(&b).Error; err != nil {
			if isDuplicate(err) {
				return apperr.Conflict("DUPLICATE_BRANCH", "A branch with code "+b.Code+" already exists.")
			}
			return err
		}
		return audit.Write(ctx, tx, actor, audit.Entry{Action: ActionUpdate, EntityType: "branch", EntityID: b.ID,
			Details: map[string]any{"before": snapshot(&before), "after": snapshot(&b)}})
	})
	if err != nil {
		return nil, err
	}
	return s.Get(ctx, actor, id)
}

// AssignAdmin makes employeeID the branch's only admin (nil = no admin). The
// employee moves to this branch with the branch_admin role; a replaced admin
// becomes regular staff of the branch.
func (s *Service) AssignAdmin(ctx context.Context, actor *auth.Actor, branchID uint64, employeeID *uint64) (*models.Branch, error) {
	if !actor.Has(auth.PermBranchesManage) {
		return nil, apperr.Forbidden("Only the owner can assign branch admins.")
	}
	err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var b models.Branch
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).First(&b, branchID).Error; err != nil {
			return apperr.NotFound("BRANCH_NOT_FOUND", "Branch not found.")
		}
		var next *models.Employee
		if employeeID != nil {
			var e models.Employee
			if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).First(&e, *employeeID).Error; err != nil || !e.IsActive() {
				return apperr.BadRequest("INVALID_EMPLOYEE", "Choose an active employee.")
			}
			if e.RoleID == models.RoleSuperAdmin {
				return apperr.BadRequest("INVALID_EMPLOYEE", "The owner already manages every branch.")
			}
			var other models.Branch
			if tx.Where("admin_employee_id = ? AND id <> ?", e.ID, b.ID).First(&other).Error == nil {
				return apperr.Conflict("ALREADY_BRANCH_ADMIN", e.FullName+" is already the admin of "+other.Name+". Unassign them there first.")
			}
			next = &e
		}
		prevID := b.AdminEmployeeID
		if prevID != nil && (next == nil || *prevID != next.ID) {
			if err := tx.Model(&models.Branch{}).Where("id = ?", b.ID).Update("admin_employee_id", nil).Error; err != nil {
				return err
			}
			if err := tx.Model(&models.Employee{}).Where("id = ? AND role_id = ?", *prevID, models.RoleBranchAdmin).
				Update("role_id", models.RoleStaff).Error; err != nil {
				return err
			}
		}
		if next != nil {
			if err := tx.Model(&models.Employee{}).Where("id = ?", next.ID).
				Updates(map[string]any{"role_id": models.RoleBranchAdmin, "branch_id": b.ID}).Error; err != nil {
				return err
			}
			if err := tx.Model(&models.Branch{}).Where("id = ?", b.ID).Update("admin_employee_id", next.ID).Error; err != nil {
				return err
			}
		}
		details := map[string]any{"branch": b.Code, "previous_admin_id": prevID, "admin_id": employeeID}
		var target uint64
		if next != nil {
			target = next.ID
		}
		return audit.Write(ctx, tx, actor, audit.Entry{Action: ActionAssignAdmin, EntityType: "branch", EntityID: b.ID, TargetEmployeeID: target, Details: details})
	})
	if err != nil {
		return nil, err
	}
	return s.Get(ctx, actor, branchID)
}

func apply(b *models.Branch, in Input) error {
	if in.Code != nil {
		code := strings.ToUpper(strings.TrimSpace(*in.Code))
		if !codeRe.MatchString(code) {
			return apperr.BadRequest("INVALID_CODE", "Branch code must be 2–32 letters, digits, '-' or '_'.")
		}
		b.Code = code
	}
	if in.Name != nil {
		name := strings.TrimSpace(*in.Name)
		if name == "" || len(name) > 150 {
			return apperr.BadRequest("INVALID_NAME", "Branch name is required (max 150 characters).")
		}
		b.Name = name
	}
	if in.Address != nil {
		if len(*in.Address) > 255 {
			return apperr.BadRequest("INVALID_ADDRESS", "Address is too long.")
		}
		b.Address = strings.TrimSpace(*in.Address)
	}
	if in.Status != nil {
		st := strings.ToUpper(*in.Status)
		if st != models.EmployeeActive && st != models.EmployeeInactive {
			return apperr.BadRequest("INVALID_STATUS", "Status must be ACTIVE or INACTIVE.")
		}
		b.Status = st
	}
	if in.UseCompanyOffice {
		b.OfficeLatitude, b.OfficeLongitude, b.AllowedRadiusMeters = nil, nil, nil
	} else if in.OfficeLatitude != nil || in.OfficeLongitude != nil || in.AllowedRadiusMeters != nil {
		if in.OfficeLatitude == nil || in.OfficeLongitude == nil || in.AllowedRadiusMeters == nil {
			return apperr.BadRequest("INVALID_OFFICE", "Set latitude, longitude and radius together.")
		}
		if *in.OfficeLatitude < -90 || *in.OfficeLatitude > 90 || *in.OfficeLongitude < -180 || *in.OfficeLongitude > 180 {
			return apperr.BadRequest("INVALID_OFFICE", "Latitude must be -90…90 and longitude -180…180.")
		}
		if *in.AllowedRadiusMeters < 10 || *in.AllowedRadiusMeters > 100000 {
			return apperr.BadRequest("INVALID_OFFICE", "Radius must be between 10 m and 100 km.")
		}
		b.OfficeLatitude, b.OfficeLongitude, b.AllowedRadiusMeters = in.OfficeLatitude, in.OfficeLongitude, in.AllowedRadiusMeters
	}
	if in.TelegramGroupID != nil {
		b.TelegramGroupID = *in.TelegramGroupID
	}
	if in.TelegramReviewChatID != nil {
		b.TelegramReviewChatID = *in.TelegramReviewChatID
	}
	return nil
}

func snapshot(b *models.Branch) map[string]any {
	return map[string]any{"code": b.Code, "name": b.Name, "address": b.Address, "status": b.Status,
		"office_latitude": b.OfficeLatitude, "office_longitude": b.OfficeLongitude, "allowed_radius_meters": b.AllowedRadiusMeters,
		"telegram_group_id": b.TelegramGroupID, "telegram_review_chat_id": b.TelegramReviewChatID}
}

// countStaff fills StaffCount (active employees) for each branch.
func (s *Service) countStaff(ctx context.Context, bs []models.Branch) error {
	if len(bs) == 0 {
		return nil
	}
	type row struct {
		BranchID uint64
		N        int64
	}
	var rows []row
	if err := s.db.WithContext(ctx).Model(&models.Employee{}).Select("branch_id, COUNT(*) AS n").
		Where("branch_id IS NOT NULL AND deleted_at IS NULL").Group("branch_id").Scan(&rows).Error; err != nil {
		return err
	}
	n := map[uint64]int64{}
	for _, r := range rows {
		n[r.BranchID] = r.N
	}
	for i := range bs {
		bs[i].StaffCount = n[bs[i].ID]
	}
	return nil
}

func isDuplicate(err error) bool {
	var me *mysqldrv.MySQLError
	return errors.As(err, &me) && me.Number == 1062
}
