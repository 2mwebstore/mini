package access

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"slices"
	"strings"
	"time"

	"attendance/backend/internal/apperr"
	"attendance/backend/internal/audit"
	"attendance/backend/internal/auth"
	"attendance/backend/internal/employee"
	"attendance/backend/internal/models"

	mysqldrv "github.com/go-sql-driver/mysql"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// ---------- system users ----------
//
// A system user is an admin panel account: a person record of kind SYSTEM with a
// login. System users are not employees — they never check in, take leave or
// appear in staff lists, reports or schedules. Someone who also works as staff has
// a separate employee record.

// SystemUser is an admin account with its role and branch access.
type SystemUser struct {
	ID             uint64     `json:"id"`        // the login
	PersonID       uint64     `json:"person_id"` // their person record (audit, reviews, branch admin)
	FullName       string     `json:"full_name"`
	Email          string     `json:"email"`
	Phone          string     `json:"phone"`
	Status         string     `json:"status"` // ACTIVE | INACTIVE (disabled)
	LastLoginAt    *time.Time `json:"last_login_at"`
	CreatedAt      time.Time  `json:"created_at"`
	RoleID         uint8      `json:"role_id"`
	Role           string     `json:"role"`
	RoleName       string     `json:"role_name"`
	BranchID       *uint64    `json:"branch_id"` // home branch, for branch-level roles
	BranchName     string     `json:"branch_name"`
	TelegramUserID *int64     `json:"telegram_user_id"` // optional: review notifications and the Mini App Team tab
	// Branch access: Scope "all" (company-wide, none assigned), "branches" (BranchIDs),
	// "team" or "none" (the role has no admin panel powers).
	Scope     string   `json:"scope"`
	BranchIDs []uint64 `json:"branch_ids"`
	Self      bool     `json:"self"`
}

// Users lists the admin accounts the actor manages: every one, or — for someone
// limited to their branches' accounts — those working only in their branches.
func (s *Service) Users(ctx context.Context, actor *auth.Actor) ([]SystemUser, error) {
	if !actor.ManagesUsers() {
		return nil, apperr.ErrForbidden
	}
	var users []models.User
	if err := s.db.WithContext(ctx).Preload("Employee").Preload("Employee.Role").Preload("Employee.Branch").Preload("Employee.TelegramAccount").
		Joins("JOIN employees e ON e.id = users.employee_id AND e.deleted_at IS NULL AND e.kind = ?", models.KindSystem).
		Order("e.full_name").Find(&users).Error; err != nil {
		return nil, err
	}
	var links []models.UserBranch
	if err := s.db.WithContext(ctx).Order("branch_id").Find(&links).Error; err != nil {
		return nil, err
	}
	byPerson := map[uint64][]uint64{}
	for _, l := range links {
		byPerson[l.EmployeeID] = append(byPerson[l.EmployeeID], l.BranchID)
	}
	out := make([]SystemUser, 0, len(users))
	for _, u := range users {
		if u.Employee == nil || !actor.ManagesUser(u.Employee, byPerson[u.EmployeeID]) {
			continue
		}
		out = append(out, systemUser(&u, byPerson[u.EmployeeID], actor))
	}
	return out, nil
}

func systemUser(u *models.User, branches []uint64, actor *auth.Actor) SystemUser {
	su := SystemUser{ID: u.ID, PersonID: u.EmployeeID, Email: u.Email, Status: u.Status, LastLoginAt: u.LastLoginAt, CreatedAt: u.CreatedAt,
		BranchIDs: branches, Self: u.EmployeeID == actor.ID()}
	if su.BranchIDs == nil {
		su.BranchIDs = []uint64{}
	}
	e := u.Employee
	if e == nil {
		return su
	}
	su.FullName, su.Phone, su.RoleID, su.BranchID = e.FullName, e.Phone, e.RoleID, e.BranchID
	su.Role = auth.RoleCode(e.RoleID)
	if e.Role != nil {
		su.RoleName = e.Role.Name
	}
	if e.Branch != nil {
		su.BranchName = e.Branch.Name
	}
	if e.TelegramAccount != nil {
		id := e.TelegramAccount.TelegramUserID
		su.TelegramUserID = &id
	}
	a := &auth.Actor{Employee: *e, Branches: branches}
	switch all, ids := a.BranchScope(); {
	case !auth.PanelAccess(e.RoleID):
		su.Scope = "none"
	case all:
		su.Scope = "all"
	case len(ids) > 0:
		su.Scope, su.BranchIDs = "branches", ids
	default:
		su.Scope = "team"
	}
	return su
}

type UserInput struct {
	FullName       *string `json:"full_name"`
	Email          *string `json:"email"`
	Password       *string `json:"password"` // create only (reset: ResetPassword)
	Phone          *string `json:"phone"`
	Status         *string `json:"status"`           // ACTIVE | INACTIVE
	Role           *string `json:"role"`             // role code: an admin role
	BranchID       *uint64 `json:"branch_id"`        // home branch for branch-level roles (0 = none)
	TelegramUserID *int64  `json:"telegram_user_id"` // 0 unlinks
}

var emailRe = regexp.MustCompile(`^[^\s@]+@[^\s@]+\.[^\s@]+$`)

// CreateUser adds an admin panel account.
func (s *Service) CreateUser(ctx context.Context, actor *auth.Actor, in UserInput) (*SystemUser, error) {
	if !actor.ManagesUsers() {
		return nil, apperr.ErrForbidden
	}
	if in.FullName == nil || in.Email == nil || in.Password == nil || in.Role == nil {
		return nil, apperr.BadRequest("USER_INCOMPLETE", "Name, email, password and role are required.")
	}
	name, err := checkName(*in.FullName)
	if err != nil {
		return nil, err
	}
	email, err := checkEmail(*in.Email)
	if err != nil {
		return nil, err
	}
	hash, err := auth.HashPassword(*in.Password)
	if err != nil {
		return nil, err
	}
	p := models.Employee{Kind: models.KindSystem, FullName: name, Email: email, Status: models.EmployeeActive,
		EmployeeCode: fmt.Sprintf("SU-T%d", time.Now().UnixNano()%1e15)} // replaced by SU-<id> below
	if in.Phone != nil {
		p.Phone = strings.TrimSpace(*in.Phone)
	}
	var u models.User
	err = s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		roleID, err := s.checkRole(actor, *in.Role)
		if err != nil {
			return err
		}
		p.RoleID = roleID
		if err := s.placeBranch(tx, actor, &p, in.BranchID); err != nil {
			return err
		}
		if err := roleFits(&p); err != nil {
			return err
		}
		if err := tx.Omit(clause.Associations).Create(&p).Error; err != nil {
			return err
		}
		if err := tx.Model(&p).Update("employee_code", fmt.Sprintf("SU-%d", p.ID)).Error; err != nil {
			return err
		}
		u = models.User{EmployeeID: p.ID, Email: email, PasswordHash: hash, Status: models.EmployeeActive}
		if err := tx.Create(&u).Error; err != nil {
			if isDuplicate(err) {
				return apperr.Conflict("EMAIL_TAKEN", "Another system user already uses this email.")
			}
			return err
		}
		if err := s.claimBranchAdmin(tx, &p, nil); err != nil {
			return err
		}
		if in.TelegramUserID != nil && *in.TelegramUserID != 0 {
			if err := employee.LinkTelegram(tx, p.ID, *in.TelegramUserID, ""); err != nil {
				return err
			}
		}
		return audit.Write(ctx, tx, actor, audit.Entry{Action: audit.ActionUserCreate, EntityType: "user", EntityID: u.ID, TargetEmployeeID: p.ID,
			Details: map[string]any{"name": name, "email": email, "role": auth.RoleCode(p.RoleID), "branch_id": p.BranchID}})
	})
	if err != nil {
		return nil, err
	}
	return s.user(ctx, actor, u.ID)
}

// UpdateUser changes an account's name, email, phone, status, role, home branch or Telegram link.
func (s *Service) UpdateUser(ctx context.Context, actor *auth.Actor, id uint64, in UserInput) (*SystemUser, error) {
	if !actor.ManagesUsers() {
		return nil, apperr.ErrForbidden
	}
	err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		u, p, err := s.load(tx, id, true)
		if err != nil {
			return err
		}
		if err := s.manages(ctx, tx, actor, p); err != nil {
			return err
		}
		self := p.ID == actor.ID()
		before := map[string]any{"name": p.FullName, "email": u.Email, "status": u.Status, "role": auth.RoleCode(p.RoleID), "branch_id": p.BranchID}
		changes := map[string]any{}
		prev := *p
		if in.FullName != nil {
			name, err := checkName(*in.FullName)
			if err != nil {
				return err
			}
			p.FullName = name
		}
		if in.Phone != nil {
			p.Phone = strings.TrimSpace(*in.Phone)
		}
		if in.Email != nil {
			email, err := checkEmail(*in.Email)
			if err != nil {
				return err
			}
			if email != u.Email {
				if err := tx.Model(u).Update("email", email).Error; err != nil {
					if isDuplicate(err) {
						return apperr.Conflict("EMAIL_TAKEN", "Another system user already uses this email.")
					}
					return err
				}
				p.Email, changes["email"] = email, email
			}
		}
		if in.Status != nil {
			st := strings.ToUpper(strings.TrimSpace(*in.Status))
			if st != models.EmployeeActive && st != models.EmployeeInactive {
				return apperr.BadRequest("INVALID_STATUS", "Status must be ACTIVE or INACTIVE.")
			}
			if st != u.Status {
				if self {
					return apperr.BadRequest("SELF_DISABLE", "You cannot disable your own account.")
				}
				if st == models.EmployeeInactive && p.RoleID == models.RoleSuperAdmin {
					if err := lastOwner(tx, p.ID); err != nil {
						return err
					}
				}
				if err := tx.Model(u).Update("status", st).Error; err != nil {
					return err
				}
				changes["status"] = st
			}
		}
		if in.Role != nil && strings.ToLower(strings.TrimSpace(*in.Role)) != auth.RoleCode(p.RoleID) {
			roleID, err := s.checkRole(actor, *in.Role)
			if err != nil {
				return err
			}
			if self {
				return apperr.BadRequest("SELF_ROLE", "You cannot change your own role.")
			}
			if p.RoleID == models.RoleSuperAdmin {
				if err := lastOwner(tx, p.ID); err != nil {
					return err
				}
			}
			p.RoleID = roleID
			changes["role"] = map[string]string{"from": before["role"].(string), "to": auth.RoleCode(roleID)}
		}
		if in.BranchID != nil || p.RoleID != prev.RoleID {
			branchIn := in.BranchID
			if branchIn == nil {
				branchIn = p.BranchID
			}
			if err := s.placeBranch(tx, actor, p, branchIn); err != nil {
				return err
			}
			if err := roleFits(p); err != nil {
				return err
			}
		}
		if err := tx.Model(&models.Employee{}).Where("id = ?", p.ID).Updates(map[string]any{
			"full_name": p.FullName, "phone": p.Phone, "email": p.Email, "role_id": p.RoleID, "branch_id": p.BranchID}).Error; err != nil {
			return err
		}
		if err := s.claimBranchAdmin(tx, p, &prev); err != nil {
			return err
		}
		if in.TelegramUserID != nil {
			if *in.TelegramUserID == 0 {
				if err := tx.Where("employee_id = ?", p.ID).Delete(&models.TelegramAccount{}).Error; err != nil {
					return err
				}
			} else if err := employee.LinkTelegram(tx, p.ID, *in.TelegramUserID, ""); err != nil {
				return err
			}
			changes["telegram_user_id"] = *in.TelegramUserID
		}
		if p.FullName != prev.FullName {
			changes["name"] = p.FullName
		}
		if !sameBranch(p.BranchID, prev.BranchID) {
			changes["branch_id"] = p.BranchID
		}
		if len(changes) == 0 {
			return nil
		}
		return audit.Write(ctx, tx, actor, audit.Entry{Action: audit.ActionUserUpdate, EntityType: "user", EntityID: u.ID, TargetEmployeeID: p.ID,
			Details: map[string]any{"before": before, "changes": changes}})
	})
	if err != nil {
		return nil, err
	}
	return s.user(ctx, actor, id)
}

func (s *Service) ResetPassword(ctx context.Context, actor *auth.Actor, id uint64, password string) error {
	if !actor.ManagesUsers() {
		return apperr.ErrForbidden
	}
	hash, err := auth.HashPassword(password)
	if err != nil {
		return err
	}
	return s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		u, p, err := s.load(tx, id, false)
		if err != nil {
			return err
		}
		if err := s.manages(ctx, tx, actor, p); err != nil {
			return err
		}
		if err := tx.Model(u).Update("password_hash", hash).Error; err != nil {
			return err
		}
		return audit.Write(ctx, tx, actor, audit.Entry{Action: audit.ActionUserPassword, EntityType: "user", EntityID: u.ID, TargetEmployeeID: p.ID})
	})
}

// DeleteUser removes an admin account: its login, branch access, Telegram link and
// branch-admin assignment. The record stays (deleted) for the audit log and reviews.
func (s *Service) DeleteUser(ctx context.Context, actor *auth.Actor, id uint64) error {
	if !actor.ManagesUsers() {
		return apperr.ErrForbidden
	}
	return s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		u, p, err := s.load(tx, id, true)
		if err != nil {
			return err
		}
		if err := s.manages(ctx, tx, actor, p); err != nil {
			return err
		}
		if p.ID == actor.ID() {
			return apperr.BadRequest("SELF_DELETE", "You cannot delete your own account.")
		}
		if p.RoleID == models.RoleSuperAdmin {
			if err := lastOwner(tx, p.ID); err != nil {
				return err
			}
		}
		if err := tx.Delete(u).Error; err != nil {
			return err
		}
		for _, del := range []any{&models.UserBranch{}, &models.TelegramAccount{}} {
			if err := tx.Where("employee_id = ?", p.ID).Delete(del).Error; err != nil {
				return err
			}
		}
		if err := tx.Model(&models.Branch{}).Where("admin_employee_id = ?", p.ID).Update("admin_employee_id", nil).Error; err != nil {
			return err
		}
		if err := tx.Model(&models.Employee{}).Where("id = ?", p.ID).
			Updates(map[string]any{"status": models.EmployeeInactive, "deleted_at": time.Now().UTC()}).Error; err != nil {
			return err
		}
		return audit.Write(ctx, tx, actor, audit.Entry{Action: audit.ActionUserDelete, EntityType: "user", EntityID: u.ID, TargetEmployeeID: p.ID,
			Details: map[string]any{"name": p.FullName, "email": u.Email, "role": auth.RoleCode(p.RoleID)}})
	})
}

// SetBranches replaces a user's branch access (empty = every branch for a company-wide
// role). Someone limited to their branches' accounts can only hand out their own branches.
func (s *Service) SetBranches(ctx context.Context, actor *auth.Actor, id uint64, branchIDs []uint64) (*SystemUser, error) {
	if !actor.ManagesUsers() {
		return nil, apperr.ErrForbidden
	}
	err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		u, p, err := s.load(tx, id, false)
		if err != nil {
			return err
		}
		if err := s.manages(ctx, tx, actor, p); err != nil {
			return err
		}
		if scoped := !actor.Has(auth.PermUsersManage); scoped {
			if p.ID == actor.ID() {
				return apperr.BadRequest("SELF_LIMIT", "You cannot change your own branch access.")
			}
			for _, b := range branchIDs {
				if !actor.Reaches(b) {
					return apperr.Forbidden("You can only give access to your own branches.")
				}
			}
			if len(branchIDs) == 0 && p.BranchID == nil {
				return apperr.BadRequest("BRANCH_REQUIRED", "Keep at least one branch: without one this account could see nothing.")
			}
		}
		if p.ID == actor.ID() && len(branchIDs) > 0 {
			return apperr.BadRequest("SELF_LIMIT", "You cannot limit your own branch access.")
		}
		ids := []uint64{}
		for _, b := range branchIDs {
			if !slices.Contains(ids, b) {
				ids = append(ids, b)
			}
		}
		if len(ids) > 0 {
			var n int64
			tx.Model(&models.Branch{}).Where("id IN ?", ids).Count(&n)
			if int(n) != len(ids) {
				return apperr.BadRequest("INVALID_BRANCH", "Unknown branch.")
			}
		}
		if err := tx.Where("employee_id = ?", p.ID).Delete(&models.UserBranch{}).Error; err != nil {
			return err
		}
		for _, b := range ids {
			if err := tx.Clauses(clause.OnConflict{DoNothing: true}).Create(&models.UserBranch{EmployeeID: p.ID, BranchID: b}).Error; err != nil {
				return err
			}
		}
		return audit.Write(ctx, tx, actor, audit.Entry{Action: audit.ActionUserBranches, EntityType: "user", EntityID: u.ID, TargetEmployeeID: p.ID,
			Details: map[string]any{"branch_ids": ids}})
	})
	if err != nil {
		return nil, err
	}
	return s.user(ctx, actor, id)
}

// ---------- helpers ----------

func (s *Service) user(ctx context.Context, actor *auth.Actor, id uint64) (*SystemUser, error) {
	var u models.User
	if err := s.db.WithContext(ctx).Preload("Employee").Preload("Employee.Role").Preload("Employee.Branch").Preload("Employee.TelegramAccount").
		First(&u, id).Error; err != nil {
		return nil, apperr.NotFound("USER_NOT_FOUND", "System user not found.")
	}
	branches, err := auth.BranchesOf(ctx, s.db, u.EmployeeID)
	if err != nil {
		return nil, err
	}
	su := systemUser(&u, branches, actor)
	return &su, nil
}

// manages: the actor may change this account (else it is "not found" to them).
func (s *Service) manages(ctx context.Context, tx *gorm.DB, actor *auth.Actor, p *models.Employee) error {
	if actor.Has(auth.PermUsersManage) {
		return nil
	}
	branches, err := auth.BranchesOf(ctx, tx, p.ID)
	if err != nil {
		return err
	}
	if !actor.ManagesUser(p, branches) {
		return apperr.NotFound("USER_NOT_FOUND", "System user not found.")
	}
	return nil
}

// load reads a system user's login and person (locked for update when lock is set).
func (s *Service) load(tx *gorm.DB, id uint64, lock bool) (*models.User, *models.Employee, error) {
	q := func() *gorm.DB { // a fresh statement per query
		if lock {
			return tx.Clauses(clause.Locking{Strength: "UPDATE"})
		}
		return tx.Session(&gorm.Session{})
	}
	var u models.User
	if err := q().First(&u, id).Error; err != nil {
		return nil, nil, apperr.NotFound("USER_NOT_FOUND", "System user not found.")
	}
	var p models.Employee
	if err := q().First(&p, u.EmployeeID).Error; err != nil || !p.IsSystemUser() || p.DeletedAt != nil {
		return nil, nil, apperr.NotFound("USER_NOT_FOUND", "System user not found.")
	}
	return &u, &p, nil
}

// checkRole: system users get admin roles; managers and staff are employees (Mini App).
func (s *Service) checkRole(actor *auth.Actor, code string) (uint8, error) {
	id, ok := auth.RoleIDFromCode(strings.ToLower(strings.TrimSpace(code)))
	if !ok {
		return 0, apperr.BadRequest("INVALID_ROLE", "Unknown role.")
	}
	if !auth.IsAdminRole(id) {
		return 0, apperr.BadRequest("STAFF_ROLE", "That role is for employees (they use the Mini App). Choose an admin role.")
	}
	if !actor.CanGrant(id) {
		if !actor.Has(auth.PermUsersManage) {
			return 0, apperr.Forbidden("You can give only branch-level roles with no more powers than your own.")
		}
		return 0, apperr.Forbidden("Only someone allowed to grant admin roles can set this role.")
	}
	return id, nil
}

// placeBranch sets a system user's home branch: none for company-wide roles; the
// branch they run for a Branch Admin (required); optional for other branch-level
// roles such as Company Admin, who work in the branches assigned to them or that
// they create.
//
// Someone limited to their branches' accounts must place each account in one of
// their branches (that is what keeps it theirs).
func (s *Service) placeBranch(tx *gorm.DB, actor *auth.Actor, p *models.Employee, branchID *uint64) error {
	scoped := !actor.Has(auth.PermUsersManage)
	if auth.IsCompanyWide(p.RoleID) {
		p.BranchID = nil
		return nil
	}
	if branchID == nil || *branchID == 0 {
		if p.RoleID == models.RoleBranchAdmin || scoped {
			return apperr.BadRequest("BRANCH_REQUIRED", "Choose the branch this account works in.")
		}
		p.BranchID = nil
		return nil
	}
	var b models.Branch
	if err := tx.First(&b, *branchID).Error; err != nil {
		return apperr.BadRequest("INVALID_BRANCH", "Unknown branch.")
	}
	if scoped && !actor.Reaches(b.ID) {
		return apperr.Forbidden("You can only add accounts to your own branches.")
	}
	p.BranchID = &b.ID
	return nil
}

// claimBranchAdmin keeps branches.admin_employee_id in step with the Branch Admin
// role: each branch has at most one, set here or on the Branches page.
func (s *Service) claimBranchAdmin(tx *gorm.DB, p *models.Employee, prev *models.Employee) error {
	wasAdmin := prev != nil && prev.RoleID == models.RoleBranchAdmin
	isAdmin := p.RoleID == models.RoleBranchAdmin
	if wasAdmin && (!isAdmin || !sameBranch(p.BranchID, prev.BranchID)) {
		if err := tx.Model(&models.Branch{}).Where("admin_employee_id = ?", p.ID).Update("admin_employee_id", nil).Error; err != nil {
			return err
		}
	}
	if !isAdmin || (wasAdmin && sameBranch(p.BranchID, prev.BranchID)) {
		return nil
	}
	var b models.Branch
	if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Preload("Admin").First(&b, *p.BranchID).Error; err != nil {
		return apperr.BadRequest("INVALID_BRANCH", "Unknown branch.")
	}
	if b.AdminEmployeeID != nil && *b.AdminEmployeeID != p.ID {
		who := "someone else"
		if b.Admin != nil {
			who = b.Admin.FullName
		}
		return apperr.Conflict("BRANCH_HAS_ADMIN", fmt.Sprintf("%s already has a branch admin (%s). Change it on the Branches page, or pick another role.", b.Name, who))
	}
	return tx.Model(&models.Branch{}).Where("id = ?", b.ID).Update("admin_employee_id", p.ID).Error
}

// roleFits: a branch's own role goes only to people of that branch.
func roleFits(p *models.Employee) error {
	if !auth.RoleFits(p.RoleID, p.BranchID) {
		return apperr.BadRequest("ROLE_OTHER_BRANCH", "That role belongs to another branch: choose its branch, or another role.")
	}
	return nil
}

// lastOwner refuses to remove the last active owner (someone must manage the company).
func lastOwner(tx *gorm.DB, personID uint64) error {
	var owners int64
	tx.Model(&models.Employee{}).Joins("JOIN users u ON u.employee_id = employees.id AND u.status = ?", models.EmployeeActive).
		Where("employees.role_id = ? AND employees.kind = ? AND employees.deleted_at IS NULL AND employees.id <> ?",
			models.RoleSuperAdmin, models.KindSystem, personID).Count(&owners)
	if owners == 0 {
		return apperr.Conflict("LAST_OWNER", "This is the only owner. Make someone else owner first.")
	}
	return nil
}

func checkName(v string) (string, error) {
	v = strings.TrimSpace(v)
	if v == "" || len([]rune(v)) > 150 {
		return "", apperr.BadRequest("INVALID_NAME", "Full name is required (max 150 characters).")
	}
	return v, nil
}

func checkEmail(v string) (string, error) {
	v = strings.ToLower(strings.TrimSpace(v))
	if !emailRe.MatchString(v) {
		return "", apperr.BadRequest("INVALID_EMAIL", "Invalid email address.")
	}
	return v, nil
}

func sameBranch(a, b *uint64) bool {
	return (a == nil && b == nil) || (a != nil && b != nil && *a == *b)
}

func isDuplicate(err error) bool {
	var me *mysqldrv.MySQLError
	return errors.As(err, &me) && me.Number == 1062
}
