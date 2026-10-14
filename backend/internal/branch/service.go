// Package branch manages branches — each runs on its own: its Telegram bot and
// chats, GPS rules and office, and (elsewhere) its own setup lists. The owner
// creates any branch and assigns one admin to each; admins with "Create branches"
// create their own (added to their branch access) and edit those they reach; a
// branch admin adjusts their branch's office, GPS and Telegram.
package branch

import (
	"context"
	"errors"
	"net/url"
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

var (
	codeRe     = regexp.MustCompile(`^[A-Za-z0-9_-]{2,32}$`)
	botTokenRe = regexp.MustCompile(`^\d{5,15}:[A-Za-z0-9_-]{30,64}$`)
)

// Input is a create/update body; nil fields are left unchanged on update.
type Input struct {
	Code                 *string   `json:"code"`
	Name                 *string   `json:"name"`
	Address              *string   `json:"address"`
	Status               *string   `json:"status"` // ACTIVE | INACTIVE
	OfficeLatitude       *float64  `json:"office_latitude"`
	OfficeLongitude      *float64  `json:"office_longitude"`
	AllowedRadiusMeters  *float64  `json:"allowed_radius_meters"`
	ClearOffice          bool      `json:"clear_office"`       // removes the office location
	GPS                  *GPSInput `json:"gps"`                // the branch's GPS rules
	TelegramBotToken     *string   `json:"telegram_bot_token"` // "" removes the bot; checked with Telegram by the handler
	MiniAppURL           *string   `json:"miniapp_url"`
	TelegramGroupID      *int64    `json:"telegram_group_id"`
	TelegramReviewChatID *int64    `json:"telegram_review_chat_id"`
	Starter              bool      `json:"starter"` // create: start with a default shift and the usual leave types

	// Set by the handler once Telegram accepted the token: the sealed token and the bot's @username.
	BotSealed   *string `json:"-"`
	BotUsername *string `json:"-"`
}

// GPSInput: whether check-ins ask for the location, require it, and refuse it
// outside the office radius.
type GPSInput struct {
	Enabled       bool `json:"enabled"`
	Required      bool `json:"required"`
	RejectOutside bool `json:"reject_outside"`
}

// ValidBotToken: the shape of a token from @BotFather ("" = remove the bot).
func ValidBotToken(v string) error {
	if v != "" && !botTokenRe.MatchString(v) {
		return apperr.BadRequest("INVALID_BOT_TOKEN", "The bot token must look like 123456789:AA… (copy it from @BotFather).")
	}
	return nil
}

// ValidMiniAppURL: Telegram only opens Mini Apps over HTTPS ("" = none).
func ValidMiniAppURL(v string) error {
	if v == "" {
		return nil
	}
	u, err := url.Parse(v)
	if err != nil || u.Scheme != "https" || u.Host == "" {
		return apperr.BadRequest("INVALID_MINIAPP_URL", "The Mini App URL must be an https:// address (Telegram only opens Mini Apps over HTTPS).")
	}
	return nil
}

// reaches: the branch is the actor's own, or within their branch access.
func reaches(actor *auth.Actor, id uint64) bool {
	return actor.InBranch(id) || (actor.Employee.BranchID != nil && *actor.Employee.BranchID == id)
}

// fill sets the fields computed for clients.
func fill(bs []models.Branch) {
	for i := range bs {
		bs[i].TelegramBotSet = bs[i].TelegramBotToken != ""
	}
}

// List returns the branches the actor may see: all for company-wide staff
// without assigned branches, otherwise their assigned branches and their own.
func (s *Service) List(ctx context.Context, actor *auth.Actor) ([]models.Branch, error) {
	q := s.db.WithContext(ctx).Preload("Admin").Order("name")
	if all, ids := actor.BranchScope(); !all {
		if b := actor.Employee.BranchID; b != nil {
			ids = append(ids, *b)
		}
		if len(ids) == 0 {
			return []models.Branch{}, nil
		}
		q = q.Where("id IN ?", ids)
	}
	out := []models.Branch{}
	if err := q.Find(&out).Error; err != nil {
		return nil, err
	}
	fill(out)
	return out, s.countStaff(ctx, out)
}

func (s *Service) Get(ctx context.Context, actor *auth.Actor, id uint64) (*models.Branch, error) {
	if !reaches(actor, id) {
		return nil, apperr.NotFound("BRANCH_NOT_FOUND", "Branch not found.")
	}
	var b models.Branch
	if err := s.db.WithContext(ctx).Preload("Admin").First(&b, id).Error; err != nil {
		return nil, apperr.NotFound("BRANCH_NOT_FOUND", "Branch not found.")
	}
	list := []models.Branch{b}
	fill(list)
	if err := s.countStaff(ctx, list); err != nil {
		return nil, err
	}
	return &list[0], nil
}

// Create adds a branch. Someone limited to some branches (e.g. a Company Admin)
// gets the new branch added to their branch access, so they reach it.
func (s *Service) Create(ctx context.Context, actor *auth.Actor, in Input) (*models.Branch, error) {
	if !actor.Has(auth.PermBranchesManage) && !actor.Has(auth.PermBranchesCreate) {
		return nil, apperr.Forbidden("You cannot create branches.")
	}
	if in.Code == nil || in.Name == nil {
		return nil, apperr.BadRequest("INVALID_BRANCH", "Code and name are required.")
	}
	off := false
	b := models.Branch{Status: models.EmployeeActive, GPSEnabled: &off, GPSRequired: &off, GPSRejectOutside: &off}
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
		if !actor.AllBranches() && actor.ID() != 0 {
			if err := tx.Create(&models.UserBranch{EmployeeID: actor.ID(), BranchID: b.ID}).Error; err != nil {
				return err
			}
		}
		if in.Starter {
			if err := starter(tx, b.ID); err != nil {
				return err
			}
		}
		return audit.Write(ctx, tx, actor, audit.Entry{Action: ActionCreate, EntityType: "branch", EntityID: b.ID,
			Details: map[string]any{"code": b.Code, "name": b.Name, "starter": in.Starter}})
	})
	if err != nil {
		return nil, err
	}
	if !actor.AllBranches() {
		actor.Branches = append(actor.Branches, b.ID) // this request sees it too
	}
	return s.Get(ctx, actor, b.ID)
}

// starter gives a new branch what it needs to run at once — a default shift and
// the usual leave types — all of it editable afterwards on the branch's lists.
func starter(tx *gorm.DB, branchID uint64) error {
	shift := models.WorkSchedule{BranchID: branchID, Name: "Office Hours", StartTime: "09:00", EndTime: "17:30", WorkType: models.WorkNormal,
		GraceMinutes: 10, WorkDays: "1,2,3,4,5", Color: "#2563eb", IsDefault: true}
	if err := tx.Create(&shift).Error; err != nil {
		return err
	}
	types := []models.LeaveType{
		{Code: "ANNUAL", Name: "Annual Leave", AnnualDays: 18, IsPaid: true, Color: "#16a34a"},
		{Code: "SICK", Name: "Sick Leave", AnnualDays: 7, IsPaid: true, AllowExceed: true, Color: "#dc2626"},
		{Code: "PERSONAL", Name: "Personal Leave", AnnualDays: 5, IsPaid: true, Color: "#7c3aed"},
		{Code: "EMERGENCY", Name: "Emergency Leave", AnnualDays: 3, IsPaid: true, AllowExceed: true, Color: "#ea580c"},
		{Code: "UNPAID", Name: "Unpaid Leave", Color: "#64748b"},
	}
	for i := range types {
		t := &types[i]
		t.BranchID, t.Approval, t.Active, t.SortOrder = branchID, models.LeaveApprovalManual, true, i+1
	}
	return tx.Create(&types).Error
}

// Update: the owner — or someone who creates branches, for those they reach — may
// change anything; a branch admin only their branch's office, GPS and Telegram.
func (s *Service) Update(ctx context.Context, actor *auth.Actor, id uint64, in Input) (*models.Branch, error) {
	full := (actor.Has(auth.PermBranchesManage) || actor.Has(auth.PermBranchesCreate)) && reaches(actor, id)
	own := actor.Has(auth.PermBranchSettings) && reaches(actor, id)
	if !full && !own {
		return nil, apperr.ErrForbidden
	}
	if !full && (in.Code != nil || in.Name != nil || in.Address != nil || in.Status != nil) {
		return nil, apperr.Forbidden("You cannot rename, move or close this branch.")
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

// AssignAdmin makes the system user employeeID the branch's only admin (nil = no
// admin). They get the branch_admin role in this branch; a replaced admin keeps
// their account but loses panel powers (Staff role) until given another role on
// System users.
//
// The owner assigns any; someone who manages their branches' admin accounts
// (users.branch) assigns in their own branches, from the accounts they manage.
func (s *Service) AssignAdmin(ctx context.Context, actor *auth.Actor, branchID uint64, employeeID *uint64) (*models.Branch, error) {
	owner := actor.Has(auth.PermBranchesManage)
	if !owner && !(actor.Has(auth.PermUsersBranch) && actor.Reaches(branchID) && actor.CanGrant(models.RoleBranchAdmin)) {
		return nil, apperr.Forbidden("Only the owner, or an admin who manages this branch's accounts, can assign its admin.")
	}
	// managed: the account is one the actor may change (always, for the owner).
	managed := func(tx *gorm.DB, e *models.Employee) bool {
		if owner {
			return true
		}
		branches, err := auth.BranchesOf(ctx, tx, e.ID)
		return err == nil && actor.ManagesUser(e, branches)
	}
	err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var b models.Branch
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).First(&b, branchID).Error; err != nil {
			return apperr.NotFound("BRANCH_NOT_FOUND", "Branch not found.")
		}
		if !owner && b.AdminEmployeeID != nil {
			var prev models.Employee
			if tx.First(&prev, *b.AdminEmployeeID).Error == nil && !managed(tx, &prev) {
				return apperr.Forbidden("This branch's admin was set by the owner; ask them to change it.")
			}
		}
		var next *models.Employee
		if employeeID != nil {
			var e models.Employee
			if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).First(&e, *employeeID).Error; err != nil || !e.IsActive() || !e.IsSystemUser() {
				return apperr.BadRequest("INVALID_EMPLOYEE", "Choose an active system user (Administration → System users).")
			}
			if e.RoleID == models.RoleSuperAdmin {
				return apperr.BadRequest("INVALID_EMPLOYEE", "The owner already manages every branch.")
			}
			if !managed(tx, &e) {
				return apperr.BadRequest("INVALID_EMPLOYEE", "Choose one of your system users (Administration → System users).")
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
	if g := in.GPS; g != nil {
		// Required / reject-outside only make sense with GPS on.
		on, req, rej := g.Enabled, g.Enabled && g.Required, g.Enabled && g.RejectOutside
		b.GPSEnabled, b.GPSRequired, b.GPSRejectOutside = &on, &req, &rej
	}
	if in.ClearOffice {
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
	if b.GPSRejectOutside != nil && *b.GPSRejectOutside && !b.HasOffice() {
		return apperr.BadRequest("OFFICE_REQUIRED", "Set the office location first: refusing check-ins outside the office needs it.")
	}
	if in.BotSealed != nil {
		b.TelegramBotToken = *in.BotSealed
		b.TelegramBotUsername = ""
		if in.BotUsername != nil {
			b.TelegramBotUsername = *in.BotUsername
		}
	}
	if in.MiniAppURL != nil {
		v := strings.TrimSpace(*in.MiniAppURL)
		if err := ValidMiniAppURL(v); err != nil {
			return err
		}
		b.MiniAppURL = v
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
		"telegram_bot": b.TelegramBotUsername, "telegram_bot_set": b.TelegramBotToken != "", "miniapp_url": b.MiniAppURL,
		"telegram_group_id": b.TelegramGroupID, "telegram_review_chat_id": b.TelegramReviewChatID,
		"gps_enabled": b.GPSEnabled, "gps_required": b.GPSRequired, "gps_reject_outside": b.GPSRejectOutside}
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
		Where("branch_id IS NOT NULL AND deleted_at IS NULL AND kind = ?", models.KindStaff).Group("branch_id").Scan(&rows).Error; err != nil {
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
