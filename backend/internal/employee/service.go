// Package employee manages staff records (the people who check in and out) and their
// Telegram identity. Admin panel accounts are system users (package access).
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
	BranchID         *uint64 `json:"branch_id"`   // required for staff, managers and branch admins
	Position         *string `json:"position"`    // by name; created if missing
	PositionID       *uint64 `json:"position_id"` // from the managed list (0 = none)
	Phone            *string `json:"phone"`
	Email            *string `json:"email"`
	JoinDate         string  `json:"join_date"`
	WorkScheduleID   *uint64 `json:"work_schedule_id"`
	ManagerID        *uint64 `json:"manager_id"`
	Role             string  `json:"role"`   // a role code (branch_admin is assigned on the Branches page)
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
	emp := models.Employee{Kind: models.KindStaff, EmployeeCode: in.EmployeeCode, FullName: in.FullName, RoleID: roleID, Status: models.EmployeeActive}
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
		defaultShift(tx, &emp)
		if err := tx.Omit(clause.Associations).Create(&emp).Error; err != nil {
			if isDuplicate(err) {
				return apperr.Conflict("DUPLICATE_CODE", "Employee ID "+emp.EmployeeCode+" already exists.")
			}
			return err
		}
		if in.TelegramUserID != nil && *in.TelegramUserID != 0 {
			if err := LinkTelegram(tx, emp.ID, *in.TelegramUserID, in.TelegramUsername); err != nil {
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
		prevBranch := emp.BranchID
		if err := s.resolveBranch(ctx, actor, emp, branchIn); err != nil {
			return nil, err
		}
		if prevBranch == nil || emp.BranchID == nil || *prevBranch != *emp.BranchID {
			// Departments, positions and shifts belong to a branch: a move drops the old ones.
			emp.DepartmentID, emp.PositionID, emp.Position, emp.WorkScheduleID = nil, nil, "", nil
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
		defaultShift(tx, emp) // after a move to another branch
		if err := tx.Omit(clause.Associations).Save(emp).Error; err != nil {
			return err
		}
		if in.TelegramUserID != nil {
			if err := LinkTelegram(tx, emp.ID, *in.TelegramUserID, in.TelegramUsername); err != nil {
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
		if err := tx.Model(&models.Employee{}).Where("id = ?", id).Updates(map[string]any{"status": models.EmployeeInactive, "deleted_at": now}).Error; err != nil {
			return err
		}
		return audit.Write(ctx, tx, actor, audit.Entry{Action: audit.ActionEmployeeDeactivate, EntityType: "employee", EntityID: id, TargetEmployeeID: id})
	})
}

func (s *Service) apply(ctx context.Context, emp *models.Employee, in Input) error {
	if err := s.resolvePosition(ctx, emp, in); err != nil {
		return err
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
		s.db.WithContext(ctx).Model(&models.WorkSchedule{}).Where("id = ? AND branch_id = ?", *in.WorkScheduleID, branchOf(emp)).Count(&n)
		if n == 0 {
			return apperr.BadRequest("INVALID_SCHEDULE", "Choose a shift of the employee's branch.")
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
		if err := tx.Where("branch_id = ?", branchOf(emp)).First(&d, *in.DepartmentID).Error; err != nil {
			return apperr.BadRequest("INVALID_DEPARTMENT", "Choose a department of the employee's branch.")
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
	d := models.Department{BranchID: branchOf(emp), Name: name}
	if err := tx.Where("branch_id = ? AND name = ?", d.BranchID, name).FirstOrCreate(&d).Error; err != nil {
		return err
	}
	emp.DepartmentID = &d.ID
	return nil
}

// resolvePosition sets the position from the managed list (by id, or by name —
// a new name is added to the list). employees.position keeps the name for display.
func (s *Service) resolvePosition(ctx context.Context, emp *models.Employee, in Input) error {
	switch {
	case in.PositionID != nil && *in.PositionID == 0:
		emp.PositionID, emp.Position = nil, ""
	case in.PositionID != nil:
		var p models.Position
		if err := s.db.WithContext(ctx).Where("branch_id = ?", branchOf(emp)).First(&p, *in.PositionID).Error; err != nil {
			return apperr.BadRequest("INVALID_POSITION", "Choose a position of the employee's branch.")
		}
		emp.PositionID, emp.Position = &p.ID, p.Name
	case in.Position != nil:
		name := strings.TrimSpace(*in.Position)
		if name == "" {
			emp.PositionID, emp.Position = nil, ""
			return nil
		}
		if len([]rune(name)) > 100 {
			return apperr.BadRequest("INVALID_POSITION", "Position name is too long.")
		}
		p := models.Position{BranchID: branchOf(emp), Name: name}
		if err := s.db.WithContext(ctx).Where("branch_id = ? AND name = ?", p.BranchID, name).FirstOrCreate(&p).Error; err != nil {
			return err
		}
		emp.PositionID, emp.Position = &p.ID, p.Name
	}
	return nil
}

func (s *Service) resolveRole(actor *auth.Actor, code string, def uint8) (uint8, error) {
	if strings.TrimSpace(code) == "" {
		return def, nil
	}
	id, ok := auth.RoleIDFromCode(strings.ToLower(strings.TrimSpace(code)))
	if !ok {
		return 0, apperr.BadRequest("INVALID_ROLE", "Unknown role.")
	}
	if models.IsAdminRole(id) {
		// Admin roles belong to system users (Administration → System users), not staff.
		return 0, apperr.BadRequest("ADMIN_ROLE", "Admin roles are for system users. Add an admin account under Administration → System users.")
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
		if branchID == nil || *branchID == 0 {
			branchID = actor.Employee.BranchID // default: the actor's own branch…
			if _, ids := actor.Reach(); branchID == nil && len(ids) == 1 {
				branchID = &ids[0] // …or the only branch they reach
			}
		}
		if branchID != nil && !actor.InBranch(*branchID) {
			return apperr.Forbidden("You can only manage staff in your assigned branches.")
		}
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

// defaultShift gives someone without a usual shift their branch's default one.
func defaultShift(tx *gorm.DB, emp *models.Employee) {
	if emp.WorkScheduleID != nil || emp.BranchID == nil {
		return
	}
	var ws models.WorkSchedule
	if tx.Where("branch_id = ? AND is_default = 1", *emp.BranchID).First(&ws).Error == nil {
		emp.WorkScheduleID = &ws.ID
	}
}

// branchOf: the employee's branch id (0 if none — then nothing of a branch matches).
func branchOf(e *models.Employee) uint64 {
	if e.BranchID == nil {
		return 0
	}
	return *e.BranchID
}

// LinkTelegram links a Telegram account to a person (an employee, or a system user
// who reviews in the Mini App). A Telegram account belongs to one person only.
func LinkTelegram(tx *gorm.DB, employeeID uint64, tgID int64, username string) error {
	if tgID <= 0 {
		return apperr.BadRequest("INVALID_TELEGRAM_ID", "Telegram User ID must be a positive number.")
	}
	var other models.TelegramAccount
	if err := tx.Where("telegram_user_id = ? AND employee_id <> ?", tgID, employeeID).First(&other).Error; err == nil {
		return apperr.Conflict("TELEGRAM_IN_USE", "This Telegram account is already linked to someone else.")
	}
	acc := models.TelegramAccount{EmployeeID: employeeID, TelegramUserID: tgID, Username: strings.TrimPrefix(strings.TrimSpace(username), "@"), LinkedAt: time.Now().UTC()}
	return tx.Clauses(clause.OnConflict{Columns: []clause.Column{{Name: "employee_id"}},
		DoUpdates: clause.AssignmentColumns([]string{"telegram_user_id", "username"})}).Create(&acc).Error
}

// get loads an employee (staff); system users are not employees and are not found here.
func (s *Service) get(ctx context.Context, id uint64) (*models.Employee, error) {
	var e models.Employee
	err := s.db.WithContext(ctx).Preload("Department").Preload("Branch").Preload("WorkSchedule").Preload("Role").Preload("TelegramAccount").
		Where("kind = ?", models.KindStaff).First(&e, id).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, apperr.NotFound("EMPLOYEE_NOT_FOUND", "Employee not found.")
	}
	if err != nil {
		return nil, err
	}
	return &e, nil
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
	PositionID      uint64 `form:"position_id"`
	BranchID        uint64 `form:"branch_id"`
	Status          string `form:"status"`
	IncludeInactive bool   `form:"include_inactive"`
}

func (s *Service) List(ctx context.Context, actor *auth.Actor, f ListFilter) ([]models.Employee, error) {
	scope, err := auth.Scope(ctx, s.db, actor)
	if err != nil {
		return nil, err
	}
	q := s.db.WithContext(ctx).Preload("Department").Preload("Branch").Preload("Role").Preload("TelegramAccount").
		Where("kind = ?", models.KindStaff).Order("employee_code")
	if scope != nil {
		q = q.Where("id IN ?", scope)
	}
	if !f.IncludeInactive {
		q = q.Where("deleted_at IS NULL")
	}
	if f.Status != "" {
		q = q.Where("status = ?", strings.ToUpper(f.Status))
	}
	if f.PositionID != 0 {
		q = q.Where("position_id = ?", f.PositionID)
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
	return out, nil
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

func (s *Service) Departments(ctx context.Context) ([]models.Department, error) {
	var ds []models.Department
	return ds, s.db.WithContext(ctx).Order("name").Find(&ds).Error
}

func escapeLike(s string) string {
	return strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`).Replace(s)
}

func isDuplicate(err error) bool {
	var me *mysqldrv.MySQLError
	return errors.As(err, &me) && me.Number == 1062
}
