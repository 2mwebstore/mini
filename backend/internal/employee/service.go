// Package employee manages staff records, their Telegram identity and admin panel logins.
package employee

import (
	"context"
	"errors"
	"regexp"
	"strings"
	"time"

	"attendance/backend/internal/apperr"
	"attendance/backend/internal/audit"
	"attendance/backend/internal/auth"
	"attendance/backend/internal/models"
	"attendance/backend/internal/timeutil"

	mysqldrv "github.com/go-sql-driver/mysql"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

type Service struct{ db *gorm.DB }

func NewService(db *gorm.DB) *Service { return &Service{db: db} }

type Input struct {
	EmployeeCode     string  `json:"employee_code"`
	FullName         string  `json:"full_name"`
	TelegramUserID   *int64  `json:"telegram_user_id"`
	TelegramUsername string  `json:"telegram_username"`
	Department       string  `json:"department"` // by name; created if missing
	DepartmentID     *uint64 `json:"department_id"`
	BranchID         *uint64 `json:"branch_id"` // required for staff, managers and branch admins
	Position         *string `json:"position"`
	Phone            *string `json:"phone"`
	Email            *string `json:"email"`
	JoinDate         string  `json:"join_date"`
	WorkScheduleID   *uint64 `json:"work_schedule_id"`
	ManagerID        *uint64 `json:"manager_id"`
	Role             string  `json:"role"`   // super_admin | hr_admin | manager | staff (branch_admin: via branches)
	Status           string  `json:"status"` // ACTIVE | INACTIVE | SUSPENDED (update only)
}

var codeRe = regexp.MustCompile(`^[A-Za-z0-9_-]{2,32}$`)
var emailRe = regexp.MustCompile(`^[^\s@]+@[^\s@]+\.[^\s@]+$`)

func (s *Service) Create(ctx context.Context, actor *auth.Actor, in Input) (*models.Employee, error) {
	if !actor.Has(auth.PermStaffManage) {
		return nil, apperr.ErrForbidden
	}
	in.EmployeeCode = strings.ToUpper(strings.TrimSpace(in.EmployeeCode))
	in.FullName = strings.TrimSpace(in.FullName)
	if !codeRe.MatchString(in.EmployeeCode) {
		return nil, apperr.BadRequest("INVALID_CODE", "Employee ID must be 2–32 letters, digits, '-' or '_'.")
	}
	if in.FullName == "" || len(in.FullName) > 150 {
		return nil, apperr.BadRequest("INVALID_NAME", "Full name is required (max 150 characters).")
	}
	roleID, err := s.resolveRole(actor, in.Role, models.RoleStaff)
	if err != nil {
		return nil, err
	}
	emp := models.Employee{EmployeeCode: in.EmployeeCode, FullName: in.FullName, RoleID: roleID, Status: models.EmployeeActive}
	if err := s.resolveBranch(ctx, actor, &emp, in.BranchID); err != nil {
		return nil, err
	}
	if err := s.apply(ctx, &emp, in); err != nil {
		return nil, err
	}
	err = s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := s.resolveDepartment(tx, &emp, in); err != nil {
			return err
		}
		if emp.WorkScheduleID == nil {
			var ws models.WorkSchedule
			if tx.Where("is_default = 1").First(&ws).Error == nil {
				emp.WorkScheduleID = &ws.ID
			}
		}
		if err := tx.Omit(clause.Associations).Create(&emp).Error; err != nil {
			if isDuplicate(err) {
				return apperr.Conflict("DUPLICATE_CODE", "Employee ID "+emp.EmployeeCode+" already exists.")
			}
			return err
		}
		if in.TelegramUserID != nil && *in.TelegramUserID != 0 {
			if err := linkTelegram(tx, emp.ID, *in.TelegramUserID, in.TelegramUsername); err != nil {
				return err
			}
		}
		return audit.Write(ctx, tx, actor, audit.Entry{Action: audit.ActionEmployeeCreate, EntityType: "employee", EntityID: emp.ID, TargetEmployeeID: emp.ID,
			Details: map[string]any{"code": emp.EmployeeCode, "name": emp.FullName, "role": auth.RoleCode(emp.RoleID), "branch_id": emp.BranchID, "telegram_user_id": in.TelegramUserID}})
	})
	if err != nil {
		return nil, err
	}
	return s.get(ctx, emp.ID)
}

func (s *Service) Update(ctx context.Context, actor *auth.Actor, id uint64, in Input) (*models.Employee, error) {
	if !actor.Has(auth.PermStaffManage) {
		return nil, apperr.ErrForbidden
	}
	emp, err := s.get(ctx, id)
	if err != nil {
		return nil, err
	}
	if !actor.CanView(emp) {
		return nil, apperr.NotFound("EMPLOYEE_NOT_FOUND", "Employee not found.")
	}
	// Branch admins edit their own branch; only super admins modify an admin's record.
	if (!actor.CanManage(emp) || models.IsAdminRole(emp.RoleID) && !actor.Has(auth.PermAssignAdminRoles)) && emp.ID != actor.ID() {
		return nil, apperr.ErrForbidden
	}
	before := map[string]any{"name": emp.FullName, "role": auth.RoleCode(emp.RoleID), "status": emp.Status, "manager_id": emp.ManagerID,
		"department_id": emp.DepartmentID, "branch_id": emp.BranchID}
	if v := strings.TrimSpace(in.FullName); v != "" {
		emp.FullName = v
	}
	if in.Role != "" && in.Role != auth.RoleCode(emp.RoleID) {
		if emp.RoleID == models.RoleBranchAdmin {
			return nil, apperr.BadRequest("BRANCH_ADMIN_ROLE", "This person is a branch admin. Unassign them on the Branches page first.")
		}
		roleID, err := s.resolveRole(actor, in.Role, emp.RoleID)
		if err != nil {
			return nil, err
		}
		if emp.ID == actor.ID() && roleID != emp.RoleID {
			return nil, apperr.BadRequest("SELF_ROLE", "You cannot change your own role.")
		}
		emp.RoleID = roleID
	}
	if in.BranchID != nil || models.IsCompanyAdmin(emp.RoleID) || emp.BranchID == nil {
		if emp.RoleID == models.RoleBranchAdmin && in.BranchID != nil && (emp.BranchID == nil || *in.BranchID != *emp.BranchID) {
			return nil, apperr.BadRequest("BRANCH_ADMIN_BRANCH", "Move a branch admin by assigning a new admin on the Branches page.")
		}
		branchIn := in.BranchID
		if branchIn == nil {
			branchIn = emp.BranchID
		}
		if err := s.resolveBranch(ctx, actor, emp, branchIn); err != nil {
			return nil, err
		}
	}
	if in.Status != "" {
		st := strings.ToUpper(in.Status)
		if st != models.EmployeeActive && st != models.EmployeeInactive && st != models.EmployeeSuspended {
			return nil, apperr.BadRequest("INVALID_STATUS", "Status must be ACTIVE, INACTIVE or SUSPENDED.")
		}
		if emp.ID == actor.ID() && st != models.EmployeeActive {
			return nil, apperr.BadRequest("SELF_STATUS", "You cannot deactivate yourself.")
		}
		emp.Status = st
	}
	if in.ManagerID != nil && *in.ManagerID == emp.ID {
		return nil, apperr.BadRequest("INVALID_MANAGER", "An employee cannot manage themselves.")
	}
	if err := s.apply(ctx, emp, in); err != nil {
		return nil, err
	}
	err = s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := s.resolveDepartment(tx, emp, in); err != nil {
			return err
		}
		if err := tx.Omit(clause.Associations).Save(emp).Error; err != nil {
			return err
		}
		if in.TelegramUserID != nil {
			if err := linkTelegram(tx, emp.ID, *in.TelegramUserID, in.TelegramUsername); err != nil {
				return err
			}
		}
		return audit.Write(ctx, tx, actor, audit.Entry{Action: audit.ActionEmployeeUpdate, EntityType: "employee", EntityID: emp.ID, TargetEmployeeID: emp.ID,
			Details: map[string]any{"before": before, "after": map[string]any{"name": emp.FullName, "role": auth.RoleCode(emp.RoleID), "status": emp.Status,
				"manager_id": emp.ManagerID, "department_id": emp.DepartmentID, "branch_id": emp.BranchID}, "telegram_user_id": in.TelegramUserID}})
	})
	if err != nil {
		return nil, err
	}
	return s.get(ctx, emp.ID)
}

// Deactivate soft-deletes: the employee can no longer use the Mini App or log in, and
// all historical attendance stays intact (foreign keys forbid hard deletes).
func (s *Service) Deactivate(ctx context.Context, actor *auth.Actor, id uint64) error {
	if !actor.Has(auth.PermStaffManage) {
		return apperr.ErrForbidden
	}
	if id == actor.ID() {
		return apperr.BadRequest("SELF_DELETE", "You cannot deactivate yourself.")
	}
	emp, err := s.get(ctx, id)
	if err != nil {
		return err
	}
	if !actor.CanView(emp) {
		return apperr.NotFound("EMPLOYEE_NOT_FOUND", "Employee not found.")
	}
	if !actor.CanManage(emp) || models.IsAdminRole(emp.RoleID) && !actor.Has(auth.PermAssignAdminRoles) {
		return apperr.ErrForbidden
	}
	now := time.Now().UTC()
	return s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		changes := map[string]any{"status": models.EmployeeInactive, "deleted_at": now}
		if emp.RoleID == models.RoleBranchAdmin {
			// The branch no longer has an admin; the owner assigns a new one.
			if err := tx.Model(&models.Branch{}).Where("admin_employee_id = ?", id).Update("admin_employee_id", nil).Error; err != nil {
				return err
			}
			changes["role_id"] = models.RoleStaff
		}
		if err := tx.Model(&models.Employee{}).Where("id = ?", id).Updates(changes).Error; err != nil {
			return err
		}
		if err := tx.Model(&models.User{}).Where("employee_id = ?", id).Update("status", models.EmployeeInactive).Error; err != nil {
			return err
		}
		return audit.Write(ctx, tx, actor, audit.Entry{Action: audit.ActionEmployeeDeactivate, EntityType: "employee", EntityID: id, TargetEmployeeID: id})
	})
}

func (s *Service) apply(ctx context.Context, emp *models.Employee, in Input) error {
	if in.Position != nil {
		emp.Position = strings.TrimSpace(*in.Position)
	}
	if in.Phone != nil {
		emp.Phone = strings.TrimSpace(*in.Phone)
	}
	if in.Email != nil {
		e := strings.ToLower(strings.TrimSpace(*in.Email))
		if e != "" && !emailRe.MatchString(e) {
			return apperr.BadRequest("INVALID_EMAIL", "Invalid email address.")
		}
		emp.Email = e
	}
	if in.JoinDate != "" {
		d, err := timeutil.ParseDate(in.JoinDate)
		if err != nil {
			return apperr.BadRequest("INVALID_DATE", err.Error())
		}
		emp.JoinDate = &d
	}
	if in.WorkScheduleID != nil {
		var n int64
		s.db.WithContext(ctx).Model(&models.WorkSchedule{}).Where("id = ?", *in.WorkScheduleID).Count(&n)
		if n == 0 {
			return apperr.BadRequest("INVALID_SCHEDULE", "Unknown work schedule.")
		}
		emp.WorkScheduleID = in.WorkScheduleID
	}
	if in.ManagerID != nil {
		if *in.ManagerID == 0 {
			emp.ManagerID = nil
		} else {
			var mgr models.Employee
			if err := s.db.WithContext(ctx).First(&mgr, *in.ManagerID).Error; err != nil || !mgr.IsActive() {
				return apperr.BadRequest("INVALID_MANAGER", "Manager not found or inactive.")
			}
			emp.ManagerID = in.ManagerID
		}
	}
	return nil
}

func (s *Service) resolveDepartment(tx *gorm.DB, emp *models.Employee, in Input) error {
	if in.DepartmentID != nil {
		if *in.DepartmentID == 0 {
			emp.DepartmentID = nil
			return nil
		}
		var d models.Department
		if err := tx.First(&d, *in.DepartmentID).Error; err != nil {
			return apperr.BadRequest("INVALID_DEPARTMENT", "Unknown department.")
		}
		emp.DepartmentID = &d.ID
		return nil
	}
	name := strings.TrimSpace(in.Department)
	if name == "" {
		return nil
	}
	if len(name) > 100 {
		return apperr.BadRequest("INVALID_DEPARTMENT", "Department name is too long.")
	}
	d := models.Department{Name: name}
	if err := tx.Where("name = ?", name).FirstOrCreate(&d).Error; err != nil {
		return err
	}
	emp.DepartmentID = &d.ID
	return nil
}

func (s *Service) resolveRole(actor *auth.Actor, code string, def uint8) (uint8, error) {
	if strings.TrimSpace(code) == "" {
		return def, nil
	}
	id, ok := auth.RoleIDFromCode(strings.ToLower(strings.TrimSpace(code)))
	if !ok {
		return 0, apperr.BadRequest("INVALID_ROLE", "Role must be super_admin, hr_admin, manager or staff.")
	}
	if id == models.RoleBranchAdmin {
		return 0, apperr.BadRequest("BRANCH_ADMIN_ROLE", "Branch admins are assigned on the Branches page.")
	}
	if models.IsAdminRole(id) && !actor.Has(auth.PermAssignAdminRoles) {
		return 0, apperr.Forbidden("Only a super admin can grant admin roles.")
	}
	return id, nil
}

// resolveBranch sets emp's branch. Company-wide admins (super/HR) have none; everyone
// else needs an active branch. A branch admin can only place people in their own branch.
func (s *Service) resolveBranch(ctx context.Context, actor *auth.Actor, emp *models.Employee, branchID *uint64) error {
	if models.IsCompanyAdmin(emp.RoleID) {
		emp.BranchID = nil
		return nil
	}
	if actor.BranchOnly() {
		if branchID != nil && actor.Employee.BranchID != nil && *branchID != *actor.Employee.BranchID {
			return apperr.Forbidden("You can only manage staff in your own branch.")
		}
		branchID = actor.Employee.BranchID
	}
	if branchID == nil || *branchID == 0 {
		return apperr.BadRequest("BRANCH_REQUIRED", "Choose a branch.")
	}
	var b models.Branch
	if err := s.db.WithContext(ctx).First(&b, *branchID).Error; err != nil {
		return apperr.BadRequest("INVALID_BRANCH", "Unknown branch.")
	}
	if b.Status != models.EmployeeActive && (emp.BranchID == nil || *emp.BranchID != b.ID) {
		return apperr.BadRequest("INACTIVE_BRANCH", "That branch is closed.")
	}
	emp.BranchID = &b.ID
	return nil
}

func linkTelegram(tx *gorm.DB, employeeID uint64, tgID int64, username string) error {
	if tgID <= 0 {
		return apperr.BadRequest("INVALID_TELEGRAM_ID", "Telegram User ID must be a positive number.")
	}
	var other models.TelegramAccount
	if err := tx.Where("telegram_user_id = ? AND employee_id <> ?", tgID, employeeID).First(&other).Error; err == nil {
		return apperr.Conflict("TELEGRAM_IN_USE", "This Telegram account is already linked to another employee.")
	}
	acc := models.TelegramAccount{EmployeeID: employeeID, TelegramUserID: tgID, Username: strings.TrimPrefix(strings.TrimSpace(username), "@"), LinkedAt: time.Now().UTC()}
	return tx.Clauses(clause.OnConflict{Columns: []clause.Column{{Name: "employee_id"}},
		DoUpdates: clause.AssignmentColumns([]string{"telegram_user_id", "username"})}).Create(&acc).Error
}

func (s *Service) get(ctx context.Context, id uint64) (*models.Employee, error) {
	var e models.Employee
	err := s.db.WithContext(ctx).Preload("Department").Preload("Branch").Preload("WorkSchedule").Preload("Role").Preload("TelegramAccount").First(&e, id).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, apperr.NotFound("EMPLOYEE_NOT_FOUND", "Employee not found.")
	}
	if err != nil {
		return nil, err
	}
	list := []models.Employee{e}
	if err := s.markLogins(ctx, list); err != nil {
		return nil, err
	}
	return &list[0], nil
}

func (s *Service) Get(ctx context.Context, actor *auth.Actor, id uint64) (*models.Employee, error) {
	e, err := s.get(ctx, id)
	if err != nil {
		return nil, err
	}
	if !actor.CanView(e) {
		return nil, apperr.NotFound("EMPLOYEE_NOT_FOUND", "Employee not found.")
	}
	return e, nil
}

type ListFilter struct {
	Q               string `form:"q"`
	DepartmentID    uint64 `form:"department_id"`
	BranchID        uint64 `form:"branch_id"`
	Status          string `form:"status"`
	IncludeInactive bool   `form:"include_inactive"`
}

func (s *Service) List(ctx context.Context, actor *auth.Actor, f ListFilter) ([]models.Employee, error) {
	scope, err := auth.Scope(ctx, s.db, actor)
	if err != nil {
		return nil, err
	}
	q := s.db.WithContext(ctx).Preload("Department").Preload("Branch").Preload("Role").Preload("TelegramAccount").Order("employee_code")
	if scope != nil {
		q = q.Where("id IN ?", scope)
	}
	if !f.IncludeInactive {
		q = q.Where("deleted_at IS NULL")
	}
	if f.Status != "" {
		q = q.Where("status = ?", strings.ToUpper(f.Status))
	}
	if f.DepartmentID != 0 {
		q = q.Where("department_id = ?", f.DepartmentID)
	}
	if f.BranchID != 0 {
		q = q.Where("branch_id = ?", f.BranchID)
	}
	if v := strings.TrimSpace(f.Q); v != "" {
		like := "%" + escapeLike(v) + "%"
		q = q.Where("employee_code = ? OR full_name LIKE ?", strings.ToUpper(v), like)
	}
	out := []models.Employee{}
	if err := q.Limit(1000).Find(&out).Error; err != nil {
		return nil, err
	}
	return out, s.markLogins(ctx, out)
}

// markLogins sets HasLogin for employees with an active admin panel account.
func (s *Service) markLogins(ctx context.Context, emps []models.Employee) error {
	if len(emps) == 0 {
		return nil
	}
	ids := make([]uint64, len(emps))
	for i := range emps {
		ids[i] = emps[i].ID
	}
	var with []uint64
	if err := s.db.WithContext(ctx).Model(&models.User{}).Where("employee_id IN ? AND status = ?", ids, models.EmployeeActive).
		Pluck("employee_id", &with).Error; err != nil {
		return err
	}
	has := map[uint64]bool{}
	for _, id := range with {
		has[id] = true
	}
	for i := range emps {
		emps[i].HasLogin = has[emps[i].ID]
	}
	return nil
}

// Find resolves "/report Dara" or "/report GT001": exact code first, then name match.
func (s *Service) Find(ctx context.Context, actor *auth.Actor, query string) ([]models.Employee, error) {
	query = strings.TrimSpace(query)
	if query == "" {
		return nil, apperr.BadRequest("EMPTY_QUERY", "Please give an employee name or ID.")
	}
	all, err := s.List(ctx, actor, ListFilter{Q: query, IncludeInactive: true})
	if err != nil {
		return nil, err
	}
	for _, e := range all {
		if strings.EqualFold(e.EmployeeCode, query) {
			return []models.Employee{e}, nil
		}
	}
	return all, nil
}

// CreateUser gives an employee an admin panel login.
func (s *Service) CreateUser(ctx context.Context, actor *auth.Actor, employeeID uint64, email, password string) (*models.User, error) {
	if !actor.Has(auth.PermAssignAdminRoles) {
		return nil, apperr.Forbidden("Only a super admin can create admin panel logins.")
	}
	email = strings.ToLower(strings.TrimSpace(email))
	if !emailRe.MatchString(email) {
		return nil, apperr.BadRequest("INVALID_EMAIL", "Invalid email address.")
	}
	hash, err := auth.HashPassword(password)
	if err != nil {
		return nil, err
	}
	if _, err := s.get(ctx, employeeID); err != nil {
		return nil, err
	}
	u := models.User{EmployeeID: employeeID, Email: email, PasswordHash: hash, Status: models.EmployeeActive}
	err = s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Create(&u).Error; err != nil {
			if isDuplicate(err) {
				return apperr.Conflict("USER_EXISTS", "A login already exists for this employee or email.")
			}
			return err
		}
		return audit.Write(ctx, tx, actor, audit.Entry{Action: audit.ActionUserCreate, EntityType: "user", EntityID: u.ID, TargetEmployeeID: employeeID,
			Details: map[string]any{"email": email}})
	})
	return &u, err
}

func (s *Service) Departments(ctx context.Context) ([]models.Department, error) {
	var ds []models.Department
	return ds, s.db.WithContext(ctx).Order("name").Find(&ds).Error
}

func (s *Service) Schedules(ctx context.Context) ([]models.WorkSchedule, error) {
	var ws []models.WorkSchedule
	return ws, s.db.WithContext(ctx).Order("id").Find(&ws).Error
}

func escapeLike(s string) string {
	return strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`).Replace(s)
}

func isDuplicate(err error) bool {
	var me *mysqldrv.MySQLError
	return errors.As(err, &me) && me.Number == 1062
}
