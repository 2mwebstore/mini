// Package access manages who may use the admin panel and what they may do there:
// roles and their permissions, system users (panel logins), and each user's
// branch access ("no branches" = every branch).
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
	"attendance/backend/internal/models"

	mysqldrv "github.com/go-sql-driver/mysql"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

type Service struct{ db *gorm.DB }

func NewService(db *gorm.DB) *Service { return &Service{db: db} }

// ---------- roles ----------

type RoleView struct {
	models.Role
	Permissions []auth.Permission `json:"permissions"`
	Employees   int64             `json:"employees"`
	Users       int64             `json:"users"`  // with a panel login
	Locked      bool              `json:"locked"` // the owner role: always every permission
}

type RoleInput struct {
	Name        *string           `json:"name"`
	Description *string           `json:"description"`
	Permissions []auth.Permission `json:"permissions"`
}

func (s *Service) Roles(ctx context.Context) ([]RoleView, error) {
	if err := auth.LoadRoles(ctx, s.db); err != nil {
		return nil, err
	}
	var roles []models.Role
	if err := s.db.WithContext(ctx).Order("is_system DESC, id").Find(&roles).Error; err != nil {
		return nil, err
	}
	var counts []struct {
		RoleID uint8
		N      int64
		U      int64
	}
	if err := s.db.WithContext(ctx).Table("employees e").Select("e.role_id, COUNT(*) AS n, COUNT(u.id) AS u").
		Joins("LEFT JOIN users u ON u.employee_id = e.id").Where("e.deleted_at IS NULL").Group("e.role_id").Scan(&counts).Error; err != nil {
		return nil, err
	}
	out := make([]RoleView, len(roles))
	for i, r := range roles {
		out[i] = RoleView{Role: r, Permissions: auth.PermissionsFor(r.ID), Locked: r.ID == models.RoleSuperAdmin}
		for _, c := range counts {
			if c.RoleID == r.ID {
				out[i].Employees, out[i].Users = c.N, c.U
			}
		}
	}
	return out, nil
}

var slugRe = regexp.MustCompile(`[^a-z0-9]+`)

func (s *Service) CreateRole(ctx context.Context, actor *auth.Actor, in RoleInput) (*models.Role, error) {
	if !actor.Has(auth.PermUsersManage) {
		return nil, apperr.ErrForbidden
	}
	if in.Name == nil {
		return nil, apperr.BadRequest("INVALID_NAME", "Role name is required.")
	}
	r := models.Role{Name: strings.TrimSpace(*in.Name)}
	if in.Description != nil {
		r.Description = strings.TrimSpace(*in.Description)
	}
	if err := checkRole(&r); err != nil {
		return nil, err
	}
	perms, err := checkPerms(in.Permissions)
	if err != nil {
		return nil, err
	}
	err = s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var maxID int
		if err := tx.Model(&models.Role{}).Select("COALESCE(MAX(id), 0)").Scan(&maxID).Error; err != nil {
			return err
		}
		if maxID >= 255 {
			return apperr.Conflict("TOO_MANY_ROLES", "The maximum number of roles has been reached.")
		}
		r.ID = uint8(max(maxID+1, 6))
		base := strings.Trim(slugRe.ReplaceAllString(strings.ToLower(r.Name), "_"), "_")
		if base == "" {
			base = "role"
		}
		r.Code = base
		for n := 2; ; n++ {
			var taken int64
			tx.Model(&models.Role{}).Where("code = ?", r.Code).Count(&taken)
			if taken == 0 {
				break
			}
			r.Code = fmt.Sprintf("%s_%d", base, n)
		}
		if err := tx.Create(&r).Error; err != nil {
			return roleWriteErr(err)
		}
		if err := setPerms(tx, r.ID, perms); err != nil {
			return err
		}
		return audit.Write(ctx, tx, actor, audit.Entry{Action: audit.ActionRoleCreate, EntityType: "role", EntityID: uint64(r.ID),
			Details: map[string]any{"name": r.Name, "permissions": perms}})
	})
	if err != nil {
		return nil, err
	}
	return &r, auth.LoadRoles(ctx, s.db)
}

func (s *Service) UpdateRole(ctx context.Context, actor *auth.Actor, id uint8, in RoleInput) (*models.Role, error) {
	if !actor.Has(auth.PermUsersManage) {
		return nil, apperr.ErrForbidden
	}
	var r models.Role
	err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.First(&r, id).Error; err != nil {
			return apperr.NotFound("ROLE_NOT_FOUND", "Role not found.")
		}
		if r.ID == models.RoleSuperAdmin && in.Permissions != nil {
			return apperr.BadRequest("ROLE_LOCKED", "The owner role always has every permission.")
		}
		before := auth.PermissionsFor(r.ID)
		if in.Name != nil {
			r.Name = strings.TrimSpace(*in.Name)
		}
		if in.Description != nil {
			r.Description = strings.TrimSpace(*in.Description)
		}
		if err := checkRole(&r); err != nil {
			return err
		}
		if err := tx.Save(&r).Error; err != nil {
			return roleWriteErr(err)
		}
		details := map[string]any{"name": r.Name}
		if in.Permissions != nil {
			perms, err := checkPerms(in.Permissions)
			if err != nil {
				return err
			}
			if r.ID == actor.Employee.RoleID && !slices.Contains(perms, auth.PermUsersManage) {
				return apperr.BadRequest("SELF_LOCKOUT", "You cannot remove “Manage system users” from your own role.")
			}
			if err := setPerms(tx, r.ID, perms); err != nil {
				return err
			}
			details["before"], details["after"] = before, perms
		}
		return audit.Write(ctx, tx, actor, audit.Entry{Action: audit.ActionRoleUpdate, EntityType: "role", EntityID: uint64(r.ID), Details: details})
	})
	if err != nil {
		return nil, err
	}
	return &r, auth.LoadRoles(ctx, s.db)
}

// DeleteRole removes a custom role nobody has.
func (s *Service) DeleteRole(ctx context.Context, actor *auth.Actor, id uint8) error {
	if !actor.Has(auth.PermUsersManage) {
		return apperr.ErrForbidden
	}
	err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var r models.Role
		if err := tx.First(&r, id).Error; err != nil {
			return apperr.NotFound("ROLE_NOT_FOUND", "Role not found.")
		}
		if r.IsSystem {
			return apperr.Conflict("ROLE_SYSTEM", "Built-in roles cannot be deleted.")
		}
		var n int64
		tx.Model(&models.Employee{}).Where("role_id = ?", id).Count(&n)
		if n > 0 {
			return apperr.Conflict("ROLE_IN_USE", fmt.Sprintf("%d employee(s) have this role. Give them another role first.", n))
		}
		if err := tx.Where("role_id = ?", id).Delete(&models.RolePermission{}).Error; err != nil {
			return err
		}
		if err := tx.Delete(&r).Error; err != nil {
			return err
		}
		return audit.Write(ctx, tx, actor, audit.Entry{Action: audit.ActionRoleDelete, EntityType: "role", EntityID: uint64(id), Details: map[string]any{"name": r.Name}})
	})
	if err != nil {
		return err
	}
	return auth.LoadRoles(ctx, s.db)
}

func checkRole(r *models.Role) error {
	if r.Name == "" || len([]rune(r.Name)) > 64 {
		return apperr.BadRequest("INVALID_NAME", "Role name is required (up to 64 characters).")
	}
	if len([]rune(r.Description)) > 255 {
		return apperr.BadRequest("DESCRIPTION_TOO_LONG", "Description: at most 255 characters.")
	}
	return nil
}

func checkPerms(in []auth.Permission) ([]auth.Permission, error) {
	known := map[auth.Permission]bool{}
	for _, p := range auth.Catalog {
		known[p.Code] = true
	}
	out := []auth.Permission{}
	for _, p := range in {
		if !known[p] {
			return nil, apperr.BadRequest("INVALID_PERMISSION", fmt.Sprintf("Unknown permission %q.", p))
		}
		if !slices.Contains(out, p) {
			out = append(out, p)
		}
	}
	return out, nil
}

func setPerms(tx *gorm.DB, roleID uint8, perms []auth.Permission) error {
	if err := tx.Where("role_id = ?", roleID).Delete(&models.RolePermission{}).Error; err != nil {
		return err
	}
	for _, p := range perms {
		if err := tx.Create(&models.RolePermission{RoleID: roleID, Permission: string(p)}).Error; err != nil {
			return err
		}
	}
	return nil
}

func roleWriteErr(err error) error {
	var me *mysqldrv.MySQLError
	if errors.As(err, &me) && me.Number == 1062 {
		return apperr.Conflict("ROLE_NAME_TAKEN", "Another role already has this name.")
	}
	return err
}

// ---------- system users ----------

// SystemUser is a panel login with its person, role and branch access.
type SystemUser struct {
	ID           uint64     `json:"id"`
	EmployeeID   uint64     `json:"employee_id"`
	EmployeeCode string     `json:"employee_code"`
	FullName     string     `json:"full_name"`
	Email        string     `json:"email"`
	Status       string     `json:"status"` // ACTIVE | INACTIVE (disabled)
	LastLoginAt  *time.Time `json:"last_login_at"`
	CreatedAt    time.Time  `json:"created_at"`
	RoleID       uint8      `json:"role_id"`
	Role         string     `json:"role"`
	RoleName     string     `json:"role_name"`
	BranchID     *uint64    `json:"branch_id"` // their own branch
	BranchName   string     `json:"branch_name"`
	// Branch access: Scope "all" (company-wide, none assigned), "branches" (BranchIDs),
	// "team" (their team only) or "none" (no panel access with this role).
	Scope     string   `json:"scope"`
	BranchIDs []uint64 `json:"branch_ids"`
	Self      bool     `json:"self"`
}

func (s *Service) Users(ctx context.Context, actor *auth.Actor) ([]SystemUser, error) {
	if !actor.Has(auth.PermUsersManage) {
		return nil, apperr.ErrForbidden
	}
	var users []models.User
	if err := s.db.WithContext(ctx).Preload("Employee").Preload("Employee.Role").Preload("Employee.Branch").
		Joins("JOIN employees e ON e.id = users.employee_id AND e.deleted_at IS NULL").Order("users.email").Find(&users).Error; err != nil {
		return nil, err
	}
	var links []models.UserBranch
	if err := s.db.WithContext(ctx).Order("branch_id").Find(&links).Error; err != nil {
		return nil, err
	}
	byEmp := map[uint64][]uint64{}
	for _, l := range links {
		byEmp[l.EmployeeID] = append(byEmp[l.EmployeeID], l.BranchID)
	}
	out := make([]SystemUser, 0, len(users))
	for _, u := range users {
		out = append(out, systemUser(&u, byEmp[u.EmployeeID], actor))
	}
	return out, nil
}

func systemUser(u *models.User, branches []uint64, actor *auth.Actor) SystemUser {
	e := u.Employee
	su := SystemUser{ID: u.ID, EmployeeID: u.EmployeeID, Email: u.Email, Status: u.Status, LastLoginAt: u.LastLoginAt, CreatedAt: u.CreatedAt,
		BranchIDs: branches, Self: u.EmployeeID == actor.ID()}
	if su.BranchIDs == nil {
		su.BranchIDs = []uint64{}
	}
	if e != nil {
		su.EmployeeCode, su.FullName, su.RoleID, su.BranchID = e.EmployeeCode, e.FullName, e.RoleID, e.BranchID
		su.Role = auth.RoleCode(e.RoleID)
		if e.Role != nil {
			su.RoleName = e.Role.Name
		}
		if e.Branch != nil {
			su.BranchName = e.Branch.Name
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
	}
	return su
}

type UserInput struct {
	EmployeeID uint64  `json:"employee_id"` // create only
	Email      *string `json:"email"`
	Password   *string `json:"password"` // create only (reset: ResetPassword)
	Status     *string `json:"status"`   // ACTIVE | INACTIVE
	Role       *string `json:"role"`     // role code: changes the person's role
}

var emailRe = regexp.MustCompile(`^[^\s@]+@[^\s@]+\.[^\s@]+$`)

// CreateUser gives an employee a panel login.
func (s *Service) CreateUser(ctx context.Context, actor *auth.Actor, in UserInput) (*SystemUser, error) {
	if !actor.Has(auth.PermUsersManage) {
		return nil, apperr.ErrForbidden
	}
	var emp models.Employee
	if err := s.db.WithContext(ctx).First(&emp, in.EmployeeID).Error; err != nil || emp.DeletedAt != nil {
		return nil, apperr.NotFound("EMPLOYEE_NOT_FOUND", "Employee not found.")
	}
	if in.Email == nil || in.Password == nil {
		return nil, apperr.BadRequest("LOGIN_INCOMPLETE", "Email and password are required.")
	}
	email := strings.ToLower(strings.TrimSpace(*in.Email))
	if !emailRe.MatchString(email) {
		return nil, apperr.BadRequest("INVALID_EMAIL", "Invalid email address.")
	}
	hash, err := auth.HashPassword(*in.Password)
	if err != nil {
		return nil, err
	}
	u := models.User{EmployeeID: emp.ID, Email: email, PasswordHash: hash, Status: models.EmployeeActive}
	err = s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if in.Role != nil && *in.Role != "" {
			if err := s.setRole(ctx, tx, actor, &emp, *in.Role); err != nil {
				return err
			}
		}
		if !auth.PanelAccess(emp.RoleID) {
			return apperr.BadRequest("NO_PANEL_ROLE", "Give this person a role with admin panel access first (their role only covers their own attendance).")
		}
		if err := tx.Create(&u).Error; err != nil {
			var me *mysqldrv.MySQLError
			if errors.As(err, &me) && me.Number == 1062 {
				return apperr.Conflict("USER_EXISTS", "A login already exists for this employee or email.")
			}
			return err
		}
		return audit.Write(ctx, tx, actor, audit.Entry{Action: audit.ActionUserCreate, EntityType: "user", EntityID: u.ID, TargetEmployeeID: emp.ID,
			Details: map[string]any{"email": email, "role": auth.RoleCode(emp.RoleID)}})
	})
	if err != nil {
		return nil, err
	}
	return s.user(ctx, actor, u.ID)
}

// UpdateUser changes a login's email or status, or the person's role.
func (s *Service) UpdateUser(ctx context.Context, actor *auth.Actor, id uint64, in UserInput) (*SystemUser, error) {
	if !actor.Has(auth.PermUsersManage) {
		return nil, apperr.ErrForbidden
	}
	err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var u models.User
		if err := tx.Preload("Employee").First(&u, id).Error; err != nil || u.Employee == nil {
			return apperr.NotFound("USER_NOT_FOUND", "System user not found.")
		}
		changes := map[string]any{}
		if in.Email != nil {
			email := strings.ToLower(strings.TrimSpace(*in.Email))
			if !emailRe.MatchString(email) {
				return apperr.BadRequest("INVALID_EMAIL", "Invalid email address.")
			}
			if email != u.Email {
				if err := tx.Model(&u).Update("email", email).Error; err != nil {
					var me *mysqldrv.MySQLError
					if errors.As(err, &me) && me.Number == 1062 {
						return apperr.Conflict("EMAIL_TAKEN", "Another login already uses this email.")
					}
					return err
				}
				changes["email"] = email
			}
		}
		if in.Status != nil {
			st := strings.ToUpper(strings.TrimSpace(*in.Status))
			if st != models.EmployeeActive && st != models.EmployeeInactive {
				return apperr.BadRequest("INVALID_STATUS", "Status must be ACTIVE or INACTIVE.")
			}
			if st != u.Status {
				if u.EmployeeID == actor.ID() && st == models.EmployeeInactive {
					return apperr.BadRequest("SELF_DISABLE", "You cannot disable your own login.")
				}
				if err := tx.Model(&u).Update("status", st).Error; err != nil {
					return err
				}
				changes["status"] = st
			}
		}
		if in.Role != nil && *in.Role != auth.RoleCode(u.Employee.RoleID) {
			before := auth.RoleCode(u.Employee.RoleID)
			if err := s.setRole(ctx, tx, actor, u.Employee, *in.Role); err != nil {
				return err
			}
			changes["role"] = map[string]string{"from": before, "to": *in.Role}
		}
		if len(changes) == 0 {
			return nil
		}
		return audit.Write(ctx, tx, actor, audit.Entry{Action: audit.ActionUserUpdate, EntityType: "user", EntityID: u.ID, TargetEmployeeID: u.EmployeeID, Details: changes})
	})
	if err != nil {
		return nil, err
	}
	return s.user(ctx, actor, id)
}

// setRole changes emp's role (saved on tx), with the same rules as the employee form.
func (s *Service) setRole(ctx context.Context, tx *gorm.DB, actor *auth.Actor, emp *models.Employee, code string) error {
	id, ok := auth.RoleIDFromCode(strings.ToLower(strings.TrimSpace(code)))
	if !ok {
		return apperr.BadRequest("INVALID_ROLE", "Unknown role.")
	}
	if id == emp.RoleID {
		return nil
	}
	if id == models.RoleBranchAdmin || emp.RoleID == models.RoleBranchAdmin {
		return apperr.BadRequest("BRANCH_ADMIN_ROLE", "Branch admins are assigned and removed on the Branches page.")
	}
	if (models.IsAdminRole(id) || models.IsAdminRole(emp.RoleID)) && !actor.Has(auth.PermAssignAdminRoles) {
		return apperr.Forbidden("Only someone allowed to grant admin roles can change this role.")
	}
	if emp.ID == actor.ID() {
		return apperr.BadRequest("SELF_ROLE", "You cannot change your own role.")
	}
	if emp.RoleID == models.RoleSuperAdmin {
		var owners int64
		tx.WithContext(ctx).Model(&models.Employee{}).Where("role_id = ? AND deleted_at IS NULL AND status = ?", models.RoleSuperAdmin, models.EmployeeActive).Count(&owners)
		if owners <= 1 {
			return apperr.Conflict("LAST_OWNER", "This is the only owner. Make someone else owner first.")
		}
	}
	changes := map[string]any{"role_id": id}
	if models.IsCompanyAdmin(id) {
		changes["branch_id"] = nil // company-wide people belong to no branch
	} else if emp.BranchID == nil {
		return apperr.BadRequest("BRANCH_REQUIRED", "This role works in a branch: set the person's branch on the Employees page first.")
	}
	if err := tx.WithContext(ctx).Model(&models.Employee{}).Where("id = ?", emp.ID).Updates(changes).Error; err != nil {
		return err
	}
	emp.RoleID = id
	if models.IsCompanyAdmin(id) {
		emp.BranchID = nil
	}
	return nil
}

func (s *Service) ResetPassword(ctx context.Context, actor *auth.Actor, id uint64, password string) error {
	if !actor.Has(auth.PermUsersManage) {
		return apperr.ErrForbidden
	}
	hash, err := auth.HashPassword(password)
	if err != nil {
		return err
	}
	return s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var u models.User
		if err := tx.First(&u, id).Error; err != nil {
			return apperr.NotFound("USER_NOT_FOUND", "System user not found.")
		}
		if err := tx.Model(&u).Update("password_hash", hash).Error; err != nil {
			return err
		}
		return audit.Write(ctx, tx, actor, audit.Entry{Action: audit.ActionUserPassword, EntityType: "user", EntityID: u.ID, TargetEmployeeID: u.EmployeeID})
	})
}

// DeleteUser removes a panel login (the employee record stays).
func (s *Service) DeleteUser(ctx context.Context, actor *auth.Actor, id uint64) error {
	if !actor.Has(auth.PermUsersManage) {
		return apperr.ErrForbidden
	}
	return s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var u models.User
		if err := tx.First(&u, id).Error; err != nil {
			return apperr.NotFound("USER_NOT_FOUND", "System user not found.")
		}
		if u.EmployeeID == actor.ID() {
			return apperr.BadRequest("SELF_DELETE", "You cannot delete your own login.")
		}
		if err := tx.Delete(&u).Error; err != nil {
			return err
		}
		if err := tx.Where("employee_id = ?", u.EmployeeID).Delete(&models.UserBranch{}).Error; err != nil {
			return err
		}
		return audit.Write(ctx, tx, actor, audit.Entry{Action: audit.ActionUserDelete, EntityType: "user", EntityID: u.ID, TargetEmployeeID: u.EmployeeID,
			Details: map[string]any{"email": u.Email}})
	})
}

// SetBranches replaces a user's branch access (empty = every branch for a company-wide role).
func (s *Service) SetBranches(ctx context.Context, actor *auth.Actor, id uint64, branchIDs []uint64) (*SystemUser, error) {
	if !actor.Has(auth.PermUsersManage) {
		return nil, apperr.ErrForbidden
	}
	err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var u models.User
		if err := tx.First(&u, id).Error; err != nil {
			return apperr.NotFound("USER_NOT_FOUND", "System user not found.")
		}
		if u.EmployeeID == actor.ID() && len(branchIDs) > 0 {
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
		if err := tx.Where("employee_id = ?", u.EmployeeID).Delete(&models.UserBranch{}).Error; err != nil {
			return err
		}
		for _, b := range ids {
			if err := tx.Clauses(clause.OnConflict{DoNothing: true}).Create(&models.UserBranch{EmployeeID: u.EmployeeID, BranchID: b}).Error; err != nil {
				return err
			}
		}
		return audit.Write(ctx, tx, actor, audit.Entry{Action: audit.ActionUserBranches, EntityType: "user", EntityID: u.ID, TargetEmployeeID: u.EmployeeID,
			Details: map[string]any{"branch_ids": ids}})
	})
	if err != nil {
		return nil, err
	}
	return s.user(ctx, actor, id)
}

func (s *Service) user(ctx context.Context, actor *auth.Actor, id uint64) (*SystemUser, error) {
	var u models.User
	if err := s.db.WithContext(ctx).Preload("Employee").Preload("Employee.Role").Preload("Employee.Branch").First(&u, id).Error; err != nil {
		return nil, apperr.NotFound("USER_NOT_FOUND", "System user not found.")
	}
	branches, err := auth.BranchesOf(ctx, s.db, u.EmployeeID)
	if err != nil {
		return nil, err
	}
	su := systemUser(&u, branches, actor)
	return &su, nil
}
