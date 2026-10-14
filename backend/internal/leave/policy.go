package leave

import (
	"context"
	"errors"
	"fmt"
	"math"
	"regexp"
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

// ---------- leave types (categories, allowances, approval policy) ----------

type TypeInput struct {
	Code              *string  `json:"code"`
	Name              *string  `json:"name"`
	AnnualDays        *float64 `json:"annual_days"`
	IsPaid            *bool    `json:"is_paid"`
	Approval          *string  `json:"approval"`
	AllowExceed       *bool    `json:"allow_exceed"`
	MaxDaysPerRequest *int     `json:"max_days_per_request"`
	MinNoticeDays     *int     `json:"min_notice_days"`
	Color             *string  `json:"color"`
	Description       *string  `json:"description"`
	Active            *bool    `json:"active"`
	SortOrder         *int     `json:"sort_order"`
}

// TypeView is a leave type with how many requests use it.
type TypeView struct {
	models.LeaveType
	Requests int64 `json:"requests"`
}

var (
	typeCode = regexp.MustCompile(`^[A-Z0-9_]{2,32}$`)
	hexColor = regexp.MustCompile(`^#[0-9a-fA-F]{6}$`)
)

// AllTypes lists every leave type (inactive too) for the admin panel.
func (s *Service) AllTypes(ctx context.Context) ([]TypeView, error) {
	var ts []models.LeaveType
	if err := s.db.WithContext(ctx).Order("active DESC, sort_order, name").Find(&ts).Error; err != nil {
		return nil, err
	}
	var counts []struct {
		LeaveTypeID uint64
		N           int64
	}
	if err := s.db.WithContext(ctx).Model(&models.LeaveRequest{}).Select("leave_type_id, COUNT(*) AS n").Group("leave_type_id").Scan(&counts).Error; err != nil {
		return nil, err
	}
	n := map[uint64]int64{}
	for _, c := range counts {
		n[c.LeaveTypeID] = c.N
	}
	out := make([]TypeView, len(ts))
	for i, t := range ts {
		out[i] = TypeView{LeaveType: t, Requests: n[t.ID]}
	}
	return out, nil
}

func (s *Service) CreateType(ctx context.Context, actor *auth.Actor, in TypeInput) (*models.LeaveType, error) {
	if !actor.Has(auth.PermLeaveManage) {
		return nil, apperr.ErrForbidden
	}
	if in.Code == nil || in.Name == nil {
		return nil, apperr.BadRequest("TYPE_INCOMPLETE", "Code and name are required.")
	}
	t := models.LeaveType{Approval: models.LeaveApprovalManual, IsPaid: true, Active: true, Color: "#2563eb"}
	var maxOrder int
	s.db.WithContext(ctx).Model(&models.LeaveType{}).Select("COALESCE(MAX(sort_order), 0)").Scan(&maxOrder)
	t.SortOrder = maxOrder + 1
	if err := applyType(&t, in, true); err != nil {
		return nil, err
	}
	err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Create(&t).Error; err != nil {
			return typeWriteErr(err)
		}
		return audit.Write(ctx, tx, actor, audit.Entry{Action: audit.ActionLeaveTypeCreate, EntityType: "leave_type", EntityID: t.ID, Details: typeAudit(&t)})
	})
	return &t, err
}

func (s *Service) UpdateType(ctx context.Context, actor *auth.Actor, id uint64, in TypeInput) (*models.LeaveType, error) {
	if !actor.Has(auth.PermLeaveManage) {
		return nil, apperr.ErrForbidden
	}
	var t models.LeaveType
	err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).First(&t, id).Error; err != nil {
			return apperr.NotFound("LEAVE_TYPE_NOT_FOUND", "Leave type not found.")
		}
		before := typeAudit(&t)
		if err := applyType(&t, in, false); err != nil {
			return err
		}
		if err := tx.Save(&t).Error; err != nil {
			return typeWriteErr(err)
		}
		return audit.Write(ctx, tx, actor, audit.Entry{Action: audit.ActionLeaveTypeUpdate, EntityType: "leave_type", EntityID: t.ID,
			Details: map[string]any{"before": before, "after": typeAudit(&t)}})
	})
	return &t, err
}

// DeleteType removes a type nobody has used; a used type can only be deactivated.
func (s *Service) DeleteType(ctx context.Context, actor *auth.Actor, id uint64) error {
	if !actor.Has(auth.PermLeaveManage) {
		return apperr.ErrForbidden
	}
	return s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var t models.LeaveType
		if err := tx.First(&t, id).Error; err != nil {
			return apperr.NotFound("LEAVE_TYPE_NOT_FOUND", "Leave type not found.")
		}
		var n int64
		tx.Model(&models.LeaveRequest{}).Where("leave_type_id = ?", id).Count(&n)
		if n > 0 {
			return apperr.Conflict("TYPE_IN_USE", fmt.Sprintf("%s has %d leave requests. Turn it off instead, so history stays intact.", t.Name, n))
		}
		if err := tx.Where("leave_type_id = ?", id).Delete(&models.LeaveBalance{}).Error; err != nil {
			return err
		}
		if err := tx.Delete(&t).Error; err != nil {
			return err
		}
		return audit.Write(ctx, tx, actor, audit.Entry{Action: audit.ActionLeaveTypeDelete, EntityType: "leave_type", EntityID: id, Details: typeAudit(&t)})
	})
}

func applyType(t *models.LeaveType, in TypeInput, creating bool) error {
	if in.Code != nil {
		code := strings.ToUpper(strings.TrimSpace(*in.Code))
		if !creating && code != t.Code {
			return apperr.BadRequest("CODE_FIXED", "A leave type's code cannot be changed.")
		}
		if !typeCode.MatchString(code) {
			return apperr.BadRequest("INVALID_CODE", "Code: 2–32 capital letters, digits or _ (e.g. MATERNITY).")
		}
		t.Code = code
	}
	if in.Name != nil {
		t.Name = strings.TrimSpace(*in.Name)
	}
	if t.Name == "" || len([]rune(t.Name)) > 100 {
		return apperr.BadRequest("INVALID_NAME", "Name is required (up to 100 characters).")
	}
	if in.AnnualDays != nil {
		t.AnnualDays = *in.AnnualDays
	}
	if t.AnnualDays < 0 || t.AnnualDays > 365 || math.Mod(t.AnnualDays*2, 1) != 0 {
		return apperr.BadRequest("INVALID_ALLOWANCE", "Yearly allowance: 0–365 days, in whole or half days (0 = not counted).")
	}
	if in.IsPaid != nil {
		t.IsPaid = *in.IsPaid
	}
	if in.Approval != nil {
		t.Approval = strings.ToUpper(strings.TrimSpace(*in.Approval))
	}
	if t.Approval != models.LeaveApprovalManual && t.Approval != models.LeaveApprovalAuto {
		return apperr.BadRequest("INVALID_APPROVAL", "Approval must be MANUAL (a reviewer approves) or AUTO.")
	}
	if in.AllowExceed != nil {
		t.AllowExceed = *in.AllowExceed
	}
	if in.MaxDaysPerRequest != nil {
		t.MaxDaysPerRequest = *in.MaxDaysPerRequest
	}
	if t.MaxDaysPerRequest < 0 || t.MaxDaysPerRequest > maxRangeDays {
		return apperr.BadRequest("INVALID_MAX_DAYS", fmt.Sprintf("Maximum days per request: 0 (no limit) to %d.", maxRangeDays))
	}
	if in.MinNoticeDays != nil {
		t.MinNoticeDays = *in.MinNoticeDays
	}
	if t.MinNoticeDays < 0 || t.MinNoticeDays > 90 {
		return apperr.BadRequest("INVALID_NOTICE", "Notice period: 0–90 days.")
	}
	if in.Color != nil {
		t.Color = strings.ToLower(strings.TrimSpace(*in.Color))
	}
	if !hexColor.MatchString(t.Color) {
		return apperr.BadRequest("INVALID_COLOR", "Colour must look like #2563eb.")
	}
	if in.Description != nil {
		t.Description = strings.TrimSpace(*in.Description)
	}
	if len([]rune(t.Description)) > 255 {
		return apperr.BadRequest("DESCRIPTION_TOO_LONG", "Description: at most 255 characters.")
	}
	if in.Active != nil {
		t.Active = *in.Active
	}
	if in.SortOrder != nil {
		t.SortOrder = *in.SortOrder
	}
	return nil
}

func typeWriteErr(err error) error {
	var me *mysqldrv.MySQLError
	if errors.As(err, &me) && me.Number == 1062 {
		return apperr.Conflict("TYPE_CODE_TAKEN", "Another leave type already has this code.")
	}
	return err
}

func typeAudit(t *models.LeaveType) map[string]any {
	return map[string]any{"code": t.Code, "name": t.Name, "annual_days": t.AnnualDays, "approval": t.Approval, "allow_exceed": t.AllowExceed,
		"max_days_per_request": t.MaxDaysPerRequest, "min_notice_days": t.MinNoticeDays, "is_paid": t.IsPaid, "active": t.Active}
}

// ---------- balances ----------

// Balance is one leave type's allowance for an employee in a year. Days are
// counted by the year their request starts in.
type Balance struct {
	LeaveTypeID uint64  `json:"leave_type_id"`
	Code        string  `json:"code"`
	Name        string  `json:"name"`
	Color       string  `json:"color"`
	Year        int     `json:"year"`
	Tracked     bool    `json:"tracked"` // false: the type has no allowance (not counted)
	Allocated   float64 `json:"allocated"`
	Carried     float64 `json:"carried"`
	Used        float64 `json:"used"`      // approved
	Pending     float64 `json:"pending"`   // waiting for approval
	Remaining   float64 `json:"remaining"` // allocated + carried − used
	Available   float64 `json:"available"` // remaining − pending: what can still be requested
	Custom      bool    `json:"custom"`    // HR set this employee's entitlement
	Note        string  `json:"note,omitempty"`
}

// balances computes every active type's balance for each employee in year.
func balances(ctx context.Context, db *gorm.DB, empIDs []uint64, year int, types []models.LeaveType) (map[uint64][]Balance, error) {
	out := map[uint64][]Balance{}
	if len(empIDs) == 0 {
		return out, nil
	}
	from := time.Date(year, 1, 1, 0, 0, 0, 0, time.UTC)
	to := time.Date(year, 12, 31, 0, 0, 0, 0, time.UTC)
	var sums []struct {
		EmployeeID  uint64
		LeaveTypeID uint64
		Status      string
		Days        float64
	}
	if err := db.WithContext(ctx).Model(&models.LeaveRequest{}).Select("employee_id, leave_type_id, status, SUM(days) AS days").
		Where("employee_id IN ? AND status IN ? AND start_date BETWEEN ? AND ?", empIDs, []string{models.LeaveApproved, models.LeavePending}, from, to).
		Group("employee_id, leave_type_id, status").Scan(&sums).Error; err != nil {
		return nil, err
	}
	type key struct{ emp, typ uint64 }
	used, pending := map[key]float64{}, map[key]float64{}
	for _, r := range sums {
		if r.Status == models.LeaveApproved {
			used[key{r.EmployeeID, r.LeaveTypeID}] += r.Days
		} else {
			pending[key{r.EmployeeID, r.LeaveTypeID}] += r.Days
		}
	}
	var custom []models.LeaveBalance
	if err := db.WithContext(ctx).Where("employee_id IN ? AND year = ?", empIDs, year).Find(&custom).Error; err != nil {
		return nil, err
	}
	set := map[key]*models.LeaveBalance{}
	for i := range custom {
		set[key{custom[i].EmployeeID, custom[i].LeaveTypeID}] = &custom[i]
	}
	for _, e := range empIDs {
		for _, t := range types {
			k := key{e, t.ID}
			b := Balance{LeaveTypeID: t.ID, Code: t.Code, Name: t.Name, Color: t.Color, Year: year, Allocated: t.AnnualDays,
				Used: used[k], Pending: pending[k], Tracked: t.AnnualDays > 0}
			if c := set[k]; c != nil {
				b.Allocated, b.Carried, b.Note, b.Custom, b.Tracked = c.Allocated, c.Carried, c.Note, true, true
			}
			b.Remaining = b.Allocated + b.Carried - b.Used
			b.Available = b.Remaining - b.Pending
			out[e] = append(out[e], b)
		}
	}
	return out, nil
}

func (s *Service) activeTypes(ctx context.Context, db *gorm.DB) ([]models.LeaveType, error) {
	var ts []models.LeaveType
	return ts, db.WithContext(ctx).Where("active = 1").Order("sort_order, name").Find(&ts).Error
}

// MyBalances: the actor's own balances for year (Mini App).
func (s *Service) MyBalances(ctx context.Context, actor *auth.Actor, year int) ([]Balance, error) {
	if !actor.Has(auth.PermLeaveSelf) {
		return nil, apperr.ErrForbidden
	}
	types, err := s.activeTypes(ctx, s.db)
	if err != nil {
		return nil, err
	}
	m, err := balances(ctx, s.db, []uint64{actor.ID()}, year, types)
	if err != nil {
		return nil, err
	}
	if m[actor.ID()] == nil {
		return []Balance{}, nil
	}
	return m[actor.ID()], nil
}

type BalanceFilter struct {
	Year         int    `form:"year"`
	BranchID     uint64 `form:"branch_id"`
	DepartmentID uint64 `form:"department_id"`
	Q            string `form:"q"`
}

type EmployeeBalances struct {
	EmployeeID   uint64    `json:"employee_id"`
	EmployeeCode string    `json:"employee_code"`
	FullName     string    `json:"full_name"`
	Branch       string    `json:"branch"`
	Department   string    `json:"department"`
	CanEdit      bool      `json:"can_edit"`
	Balances     []Balance `json:"balances"`
}

// Balances lists the balances of the active employees the actor may see.
func (s *Service) Balances(ctx context.Context, actor *auth.Actor, f BalanceFilter) ([]EmployeeBalances, []models.LeaveType, error) {
	scope, err := auth.Scope(ctx, s.db, actor)
	if err != nil {
		return nil, nil, err
	}
	q := s.db.WithContext(ctx).Preload("Branch").Preload("Department").
		Where("deleted_at IS NULL AND status <> ?", models.EmployeeInactive).Order("full_name")
	if scope != nil {
		q = q.Where("id IN ?", scope)
	}
	if f.BranchID != 0 {
		q = q.Where("branch_id = ?", f.BranchID)
	}
	if f.DepartmentID != 0 {
		q = q.Where("department_id = ?", f.DepartmentID)
	}
	if t := strings.TrimSpace(f.Q); t != "" {
		like := "%" + strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`).Replace(t) + "%"
		q = q.Where("(full_name LIKE ? OR employee_code LIKE ?)", like, like)
	}
	var emps []models.Employee
	if err := q.Find(&emps).Error; err != nil {
		return nil, nil, err
	}
	types, err := s.activeTypes(ctx, s.db)
	if err != nil {
		return nil, nil, err
	}
	ids := make([]uint64, len(emps))
	for i, e := range emps {
		ids[i] = e.ID
	}
	m, err := balances(ctx, s.db, ids, f.Year, types)
	if err != nil {
		return nil, nil, err
	}
	out := make([]EmployeeBalances, 0, len(emps))
	for i := range emps {
		e := &emps[i]
		row := EmployeeBalances{EmployeeID: e.ID, EmployeeCode: e.EmployeeCode, FullName: e.FullName, CanEdit: canSetBalance(actor, e), Balances: m[e.ID]}
		if e.Branch != nil {
			row.Branch = e.Branch.Name
		}
		if e.Department != nil {
			row.Department = e.Department.Name
		}
		if row.Balances == nil {
			row.Balances = []Balance{}
		}
		out = append(out, row)
	}
	return out, types, nil
}

func canSetBalance(actor *auth.Actor, e *models.Employee) bool {
	return actor.Has(auth.PermLeaveManage) && e.ID != actor.ID() && (actor.Has(auth.PermAllView) || actor.Has(auth.PermBranchView)) && actor.CanView(e)
}

type BalanceInput struct {
	EmployeeID  uint64  `json:"employee_id"`
	LeaveTypeID uint64  `json:"leave_type_id"`
	Year        int     `json:"year"`
	Allocated   float64 `json:"allocated"`
	Carried     float64 `json:"carried"`
	Note        string  `json:"note"`
}

// SetBalance sets an employee's entitlement for a type and year.
func (s *Service) SetBalance(ctx context.Context, actor *auth.Actor, in BalanceInput) error {
	emp, err := s.balanceTarget(ctx, actor, in.EmployeeID, in.LeaveTypeID, in.Year)
	if err != nil {
		return err
	}
	for _, v := range []float64{in.Allocated, in.Carried} {
		if v < 0 || v > 365 || math.Mod(v*2, 1) != 0 {
			return apperr.BadRequest("INVALID_DAYS", "Days: 0–365, in whole or half days.")
		}
	}
	note := strings.TrimSpace(in.Note)
	if len([]rune(note)) > 255 {
		return apperr.BadRequest("NOTE_TOO_LONG", "Note: at most 255 characters.")
	}
	by := actor.ID()
	row := models.LeaveBalance{EmployeeID: emp.ID, LeaveTypeID: in.LeaveTypeID, Year: in.Year, Allocated: in.Allocated, Carried: in.Carried, Note: note, UpdatedBy: &by}
	if by == 0 {
		row.UpdatedBy = nil
	}
	return s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Clauses(clause.OnConflict{
			Columns:   []clause.Column{{Name: "employee_id"}, {Name: "leave_type_id"}, {Name: "year"}},
			DoUpdates: clause.AssignmentColumns([]string{"allocated", "carried", "note", "updated_by", "updated_at"}),
		}).Create(&row).Error; err != nil {
			return err
		}
		return audit.Write(ctx, tx, actor, audit.Entry{Action: audit.ActionLeaveBalanceSet, EntityType: "leave_balance", TargetEmployeeID: emp.ID,
			Details: map[string]any{"leave_type_id": in.LeaveTypeID, "year": in.Year, "allocated": in.Allocated, "carried": in.Carried, "note": note}})
	})
}

// ResetBalance returns an employee to the type's default allowance.
func (s *Service) ResetBalance(ctx context.Context, actor *auth.Actor, empID, typeID uint64, year int) error {
	emp, err := s.balanceTarget(ctx, actor, empID, typeID, year)
	if err != nil {
		return err
	}
	return s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Where("employee_id = ? AND leave_type_id = ? AND year = ?", emp.ID, typeID, year).Delete(&models.LeaveBalance{}).Error; err != nil {
			return err
		}
		return audit.Write(ctx, tx, actor, audit.Entry{Action: audit.ActionLeaveBalanceSet, EntityType: "leave_balance", TargetEmployeeID: emp.ID,
			Details: map[string]any{"leave_type_id": typeID, "year": year, "reset": true}})
	})
}

func (s *Service) balanceTarget(ctx context.Context, actor *auth.Actor, empID, typeID uint64, year int) (*models.Employee, error) {
	if !actor.Has(auth.PermLeaveManage) {
		return nil, apperr.ErrForbidden
	}
	if year < 2000 || year > 2100 {
		return nil, apperr.BadRequest("INVALID_YEAR", "Invalid year.")
	}
	var emp models.Employee
	if err := s.db.WithContext(ctx).First(&emp, empID).Error; err != nil {
		return nil, apperr.NotFound("EMPLOYEE_NOT_FOUND", "Employee not found.")
	}
	if !canSetBalance(actor, &emp) {
		return nil, apperr.ErrForbidden
	}
	var n int64
	s.db.WithContext(ctx).Model(&models.LeaveType{}).Where("id = ?", typeID).Count(&n)
	if n == 0 {
		return nil, apperr.BadRequest("INVALID_LEAVE_TYPE", "Unknown leave type.")
	}
	return &emp, nil
}

// checkPolicy applies the type's rules to a request of days working days.
func (s *Service) checkPolicy(ctx context.Context, tx *gorm.DB, actor *auth.Actor, lt *models.LeaveType, start, today time.Time, days float64) (*Balance, error) {
	if lt.MaxDaysPerRequest > 0 && days > float64(lt.MaxDaysPerRequest) {
		return nil, apperr.BadRequest("TOO_MANY_DAYS", fmt.Sprintf("%s allows at most %d working days per request.", lt.Name, lt.MaxDaysPerRequest))
	}
	if lt.MinNoticeDays > 0 && start.Before(today.AddDate(0, 0, lt.MinNoticeDays)) {
		return nil, apperr.BadRequest("SHORT_NOTICE", fmt.Sprintf("%s must be requested at least %d day(s) before it starts.", lt.Name, lt.MinNoticeDays))
	}
	m, err := balances(ctx, tx, []uint64{actor.ID()}, start.Year(), []models.LeaveType{*lt})
	if err != nil {
		return nil, err
	}
	b := m[actor.ID()][0]
	if b.Tracked && !lt.AllowExceed && days > b.Available {
		return &b, apperr.BadRequest("INSUFFICIENT_BALANCE", fmt.Sprintf("Not enough %s left for %d: %s available, %g requested.",
			lt.Name, start.Year(), fmtDays(b.Available), days))
	}
	return &b, nil
}

func fmtDays(d float64) string {
	if d < 0 {
		d = 0
	}
	if d == math.Trunc(d) {
		return fmt.Sprintf("%.0f day(s)", d)
	}
	return fmt.Sprintf("%.1f day(s)", d)
}
