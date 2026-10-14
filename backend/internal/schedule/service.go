// Package schedule manages shifts, the weekly roster and special (temporary)
// schedules. What an employee actually works on a date is decided in one place,
// attendance.Planner: special schedule > roster > usual shift.
package schedule

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"

	"attendance/backend/internal/apperr"
	"attendance/backend/internal/attendance"
	"attendance/backend/internal/audit"
	"attendance/backend/internal/auth"
	"attendance/backend/internal/models"
	"attendance/backend/internal/settings"
	"attendance/backend/internal/timeutil"

	mysqldrv "github.com/go-sql-driver/mysql"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

type Service struct {
	db       *gorm.DB
	settings *settings.Service
	Now      func() time.Time
}

func NewService(db *gorm.DB, st *settings.Service) *Service {
	return &Service{db: db, settings: st, Now: func() time.Time { return time.Now().UTC() }}
}

var (
	hhmm     = regexp.MustCompile(`^([01]\d|2[0-3]):[0-5]\d$`)
	hexColor = regexp.MustCompile(`^#[0-9a-fA-F]{6}$`)
)

const (
	maxGrace        = 240
	maxRosterDays   = 31
	maxRosterItems  = 500
	maxOverrideDays = 366
)

// ---------- shifts ----------

type ShiftInput struct {
	Name         *string `json:"name"`
	StartTime    *string `json:"start_time"`
	EndTime      *string `json:"end_time"`
	WorkType     *string `json:"work_type"`
	GraceMinutes *int    `json:"grace_minutes"`
	WorkDays     []int   `json:"work_days"`
	Color        *string `json:"color"`
	IsDefault    *bool   `json:"is_default"` // true makes this the company default (for staff without a shift)
}

// ShiftView is a shift with how many active employees use it as their usual shift.
type ShiftView struct {
	models.WorkSchedule
	Employees int64 `json:"employees"`
}

func (s *Service) Shifts(ctx context.Context) ([]ShiftView, error) {
	var shifts []models.WorkSchedule
	if err := s.db.WithContext(ctx).Order("is_default DESC, start_time, name").Find(&shifts).Error; err != nil {
		return nil, err
	}
	var counts []struct {
		WorkScheduleID uint64
		N              int64
	}
	if err := s.db.WithContext(ctx).Model(&models.Employee{}).Select("work_schedule_id, COUNT(*) AS n").
		Where("kind = ? AND deleted_at IS NULL AND work_schedule_id IS NOT NULL", models.KindStaff).Group("work_schedule_id").Scan(&counts).Error; err != nil {
		return nil, err
	}
	byID := map[uint64]int64{}
	for _, c := range counts {
		byID[c.WorkScheduleID] = c.N
	}
	var unassigned int64 // staff without a shift work the default one
	if err := s.db.WithContext(ctx).Model(&models.Employee{}).Where("kind = ? AND deleted_at IS NULL AND work_schedule_id IS NULL", models.KindStaff).Count(&unassigned).Error; err != nil {
		return nil, err
	}
	out := make([]ShiftView, len(shifts))
	for i, sh := range shifts {
		out[i] = ShiftView{WorkSchedule: sh, Employees: byID[sh.ID]}
		if sh.IsDefault {
			out[i].Employees += unassigned
		}
	}
	return out, nil
}

func (s *Service) CreateShift(ctx context.Context, actor *auth.Actor, in ShiftInput) (*models.WorkSchedule, error) {
	if !actor.Has(auth.PermSchedulesManage) {
		return nil, apperr.ErrForbidden
	}
	ws := models.WorkSchedule{WorkType: models.WorkNormal, WorkDays: "1,2,3,4,5", Color: "#2563eb", GraceMinutes: 10}
	if in.Name == nil || in.StartTime == nil || in.EndTime == nil {
		return nil, apperr.BadRequest("SHIFT_INCOMPLETE", "Name, start time and end time are required.")
	}
	if err := applyShift(&ws, in); err != nil {
		return nil, err
	}
	err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Create(&ws).Error; err != nil {
			return shiftWriteErr(err)
		}
		if in.IsDefault != nil && *in.IsDefault {
			if err := makeDefault(tx, ws.ID); err != nil {
				return err
			}
			ws.IsDefault = true
		}
		return audit.Write(ctx, tx, actor, audit.Entry{Action: audit.ActionShiftCreate, EntityType: "work_schedule", EntityID: ws.ID, Details: shiftAudit(&ws)})
	})
	if err != nil {
		return nil, err
	}
	return &ws, nil
}

func (s *Service) UpdateShift(ctx context.Context, actor *auth.Actor, id uint64, in ShiftInput) (*models.WorkSchedule, error) {
	if !actor.Has(auth.PermSchedulesManage) {
		return nil, apperr.ErrForbidden
	}
	var ws models.WorkSchedule
	err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).First(&ws, id).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return apperr.NotFound("SHIFT_NOT_FOUND", "Shift not found.")
			}
			return err
		}
		before := shiftAudit(&ws)
		if err := applyShift(&ws, in); err != nil {
			return err
		}
		if err := tx.Save(&ws).Error; err != nil {
			return shiftWriteErr(err)
		}
		if in.IsDefault != nil && *in.IsDefault && !ws.IsDefault {
			if err := makeDefault(tx, ws.ID); err != nil {
				return err
			}
			ws.IsDefault = true
		}
		return audit.Write(ctx, tx, actor, audit.Entry{Action: audit.ActionShiftUpdate, EntityType: "work_schedule", EntityID: ws.ID,
			Details: map[string]any{"before": before, "after": shiftAudit(&ws)}})
	})
	if err != nil {
		return nil, err
	}
	return &ws, nil
}

// DeleteShift removes a shift nobody uses (as usual shift, on the roster or in a special schedule).
func (s *Service) DeleteShift(ctx context.Context, actor *auth.Actor, id uint64) error {
	if !actor.Has(auth.PermSchedulesManage) {
		return apperr.ErrForbidden
	}
	return s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var ws models.WorkSchedule
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).First(&ws, id).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return apperr.NotFound("SHIFT_NOT_FOUND", "Shift not found.")
			}
			return err
		}
		if ws.IsDefault {
			return apperr.Conflict("SHIFT_IS_DEFAULT", "This is the default shift. Make another shift the default first.")
		}
		var staff, roster, special int64
		tx.Model(&models.Employee{}).Where("work_schedule_id = ? AND deleted_at IS NULL", id).Count(&staff)
		tx.Model(&models.RosterShift{}).Where("work_schedule_id = ?", id).Count(&roster)
		tx.Model(&models.ScheduleOverride{}).Where("work_schedule_id = ?", id).Count(&special)
		if staff+roster+special > 0 {
			return apperr.Conflict("SHIFT_IN_USE", fmt.Sprintf("This shift is in use (%d employees, %d roster days, %d special schedules). Move them to another shift first.", staff, roster, special))
		}
		// Deactivated staff may still point at it: fall back to the default shift.
		if err := tx.Model(&models.Employee{}).Where("work_schedule_id = ?", id).Update("work_schedule_id", nil).Error; err != nil {
			return err
		}
		if err := tx.Delete(&ws).Error; err != nil {
			return err
		}
		return audit.Write(ctx, tx, actor, audit.Entry{Action: audit.ActionShiftDelete, EntityType: "work_schedule", EntityID: id, Details: shiftAudit(&ws)})
	})
}

func applyShift(ws *models.WorkSchedule, in ShiftInput) error {
	if in.Name != nil {
		ws.Name = strings.TrimSpace(*in.Name)
	}
	if ws.Name == "" || len([]rune(ws.Name)) > 100 {
		return apperr.BadRequest("INVALID_NAME", "Shift name is required (up to 100 characters).")
	}
	if in.StartTime != nil {
		ws.StartTime = strings.TrimSpace(*in.StartTime)
	}
	if in.EndTime != nil {
		ws.EndTime = strings.TrimSpace(*in.EndTime)
	}
	if in.WorkType != nil {
		ws.WorkType = strings.ToUpper(strings.TrimSpace(*in.WorkType))
	}
	if err := checkHours(ws.StartTime, ws.EndTime, ws.WorkType); err != nil {
		return err
	}
	if in.GraceMinutes != nil {
		ws.GraceMinutes = *in.GraceMinutes
	}
	if ws.GraceMinutes < 0 || ws.GraceMinutes > maxGrace {
		return apperr.BadRequest("INVALID_GRACE", fmt.Sprintf("Grace period must be 0–%d minutes.", maxGrace))
	}
	if in.WorkDays != nil {
		days, err := workDays(in.WorkDays, false)
		if err != nil {
			return err
		}
		ws.WorkDays = days
	}
	if in.Color != nil {
		ws.Color = strings.ToLower(strings.TrimSpace(*in.Color))
	}
	if !hexColor.MatchString(ws.Color) {
		return apperr.BadRequest("INVALID_COLOR", "Colour must look like #2563eb.")
	}
	return nil
}

// checkHours validates a start/end pair for its work type.
func checkHours(start, end, workType string) error {
	if !hhmm.MatchString(start) || !hhmm.MatchString(end) {
		return apperr.BadRequest("INVALID_TIME", "Times must be HH:MM (24-hour), e.g. 08:00.")
	}
	switch workType {
	case models.WorkNormal:
		if end <= start {
			return apperr.BadRequest("INVALID_HOURS", "A normal (same-day) shift must end after it starts. For a night shift ending the next day, choose the cross-day work type.")
		}
	case models.WorkCrossDay:
		if end > start {
			return apperr.BadRequest("INVALID_HOURS", "A cross-day shift ends the next day, so its end time must be earlier than its start (e.g. 22:00–06:00).")
		}
	default:
		return apperr.BadRequest("INVALID_WORK_TYPE", "Work type must be NORMAL (same day) or CROSS_DAY (ends the next day).")
	}
	return nil
}

// workDays turns [1,2,3] into "1,2,3" (ISO weekdays); allowEmpty is for "use the shift's days".
func workDays(days []int, allowEmpty bool) (string, error) {
	set := map[int]bool{}
	for _, d := range days {
		if d < 1 || d > 7 {
			return "", apperr.BadRequest("INVALID_WORK_DAYS", "Work days are 1 (Monday) to 7 (Sunday).")
		}
		set[d] = true
	}
	if len(set) == 0 && !allowEmpty {
		return "", apperr.BadRequest("INVALID_WORK_DAYS", "Choose at least one work day.")
	}
	parts := make([]string, 0, len(set))
	for d := 1; d <= 7; d++ {
		if set[d] {
			parts = append(parts, strconv.Itoa(d))
		}
	}
	return strings.Join(parts, ","), nil
}

func makeDefault(tx *gorm.DB, id uint64) error {
	if err := tx.Model(&models.WorkSchedule{}).Where("id <> ?", id).Update("is_default", false).Error; err != nil {
		return err
	}
	return tx.Model(&models.WorkSchedule{}).Where("id = ?", id).Update("is_default", true).Error
}

func shiftWriteErr(err error) error {
	var me *mysqldrv.MySQLError
	if errors.As(err, &me) && me.Number == 1062 {
		return apperr.Conflict("SHIFT_NAME_TAKEN", "Another shift already has this name.")
	}
	return err
}

func shiftAudit(ws *models.WorkSchedule) map[string]any {
	return map[string]any{"name": ws.Name, "start": ws.StartTime, "end": ws.EndTime, "work_type": ws.WorkType,
		"grace_minutes": ws.GraceMinutes, "work_days": ws.WorkDays, "is_default": ws.IsDefault}
}

// ---------- employees in scope ----------

type StaffFilter struct {
	BranchID     uint64
	DepartmentID uint64
	EmployeeID   uint64
	Q            string // name or employee ID
}

// staff returns the active employees the actor may see, matching f.
func (s *Service) staff(ctx context.Context, actor *auth.Actor, f StaffFilter) ([]models.Employee, error) {
	scope, err := auth.Scope(ctx, s.db, actor)
	if err != nil {
		return nil, err
	}
	q := s.db.WithContext(ctx).Preload("Branch").Preload("Department").
		Where("kind = ? AND deleted_at IS NULL AND status <> ?", models.KindStaff, models.EmployeeInactive).Order("full_name")
	if scope != nil {
		q = q.Where("id IN ?", scope)
	}
	if f.BranchID != 0 {
		q = q.Where("branch_id = ?", f.BranchID)
	}
	if f.DepartmentID != 0 {
		q = q.Where("department_id = ?", f.DepartmentID)
	}
	if f.EmployeeID != 0 {
		q = q.Where("id = ?", f.EmployeeID)
	}
	if t := strings.TrimSpace(f.Q); t != "" {
		like := "%" + strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`).Replace(t) + "%"
		q = q.Where("(full_name LIKE ? OR employee_code LIKE ?)", like, like)
	}
	var emps []models.Employee
	return emps, q.Find(&emps).Error
}

// ---------- roster ----------

type RosterCell struct {
	Date time.Time `json:"date"`
	attendance.Day
	Holiday string `json:"holiday,omitempty"`
	Leave   string `json:"leave,omitempty"` // approved leave type on this date
}

type RosterRow struct {
	EmployeeID   uint64       `json:"employee_id"`
	EmployeeCode string       `json:"employee_code"`
	FullName     string       `json:"full_name"`
	Branch       string       `json:"branch"`
	Department   string       `json:"department"`
	UsualShift   string       `json:"usual_shift"`
	CanEdit      bool         `json:"can_edit"`
	Cells        []RosterCell `json:"cells"`
}

type Roster struct {
	From   time.Time             `json:"from"`
	To     time.Time             `json:"to"`
	Days   []time.Time           `json:"days"`
	Rows   []RosterRow           `json:"rows"`
	Shifts []models.WorkSchedule `json:"shifts"`
	Loc    *time.Location        `json:"-"`
}

// Roster is the schedule matrix (employees × dates) for from..to (at most 31 days).
func (s *Service) Roster(ctx context.Context, actor *auth.Actor, from, to time.Time, f StaffFilter) (*Roster, error) {
	if to.Before(from) {
		return nil, apperr.BadRequest("INVALID_RANGE", "End date must be on or after start date.")
	}
	if to.Sub(from) >= maxRosterDays*24*time.Hour {
		return nil, apperr.BadRequest("RANGE_TOO_LONG", fmt.Sprintf("The roster shows at most %d days.", maxRosterDays))
	}
	st, err := s.settings.Get(ctx)
	if err != nil {
		return nil, err
	}
	emps, err := s.staff(ctx, actor, f)
	if err != nil {
		return nil, err
	}
	out := &Roster{From: from, To: to, Days: []time.Time{}, Rows: []RosterRow{}, Shifts: []models.WorkSchedule{}, Loc: st.Location}
	for d := from; !d.After(to); d = d.AddDate(0, 0, 1) {
		out.Days = append(out.Days, d)
	}
	if err := s.db.WithContext(ctx).Order("start_time, name").Find(&out.Shifts).Error; err != nil {
		return nil, err
	}
	ids := make([]uint64, len(emps))
	for i, e := range emps {
		ids[i] = e.ID
	}
	plan, err := attendance.LoadPlanner(ctx, s.db, ids, from, to)
	if err != nil {
		return nil, err
	}
	holidays := map[string]string{}
	var hs []models.Holiday
	if err := s.db.WithContext(ctx).Where("date BETWEEN ? AND ?", from, to).Find(&hs).Error; err != nil {
		return nil, err
	}
	for _, h := range hs {
		holidays[h.Date.Format("2006-01-02")] = h.Name
	}
	var leaves []models.LeaveRequest
	if len(ids) > 0 {
		if err := s.db.WithContext(ctx).Preload("LeaveType").Where("employee_id IN ? AND status = ? AND start_date <= ? AND end_date >= ?",
			ids, models.LeaveApproved, to, from).Find(&leaves).Error; err != nil {
			return nil, err
		}
	}
	for i := range emps {
		e := &emps[i]
		row := RosterRow{EmployeeID: e.ID, EmployeeCode: e.EmployeeCode, FullName: e.FullName, UsualShift: plan.Usual(e).Name,
			CanEdit: actor.CanManage(e), Cells: make([]RosterCell, 0, len(out.Days))}
		if e.Branch != nil {
			row.Branch = e.Branch.Name
		}
		if e.Department != nil {
			row.Department = e.Department.Name
		}
		for _, d := range out.Days {
			cell := RosterCell{Date: d, Day: plan.Day(e, d), Holiday: holidays[d.Format("2006-01-02")]}
			for _, l := range leaves {
				if l.EmployeeID == e.ID && !d.Before(l.StartDate) && !d.After(l.EndDate) {
					cell.Leave = "Leave"
					if l.LeaveType != nil {
						cell.Leave = l.LeaveType.Name
					}
				}
			}
			row.Cells = append(row.Cells, cell)
		}
		out.Rows = append(out.Rows, row)
	}
	return out, nil
}

// RosterItem sets one employee-date: a shift, a day off, or back to the usual pattern.
type RosterItem struct {
	EmployeeID     uint64  `json:"employee_id"`
	Date           string  `json:"date"`
	WorkScheduleID *uint64 `json:"work_schedule_id"`
	Off            bool    `json:"off"`
	Reset          bool    `json:"reset"`
}

func (s *Service) SetRoster(ctx context.Context, actor *auth.Actor, items []RosterItem) (int, error) {
	if len(items) == 0 {
		return 0, apperr.BadRequest("NO_ITEMS", "Nothing to change.")
	}
	if len(items) > maxRosterItems {
		return 0, apperr.BadRequest("TOO_MANY_ITEMS", fmt.Sprintf("At most %d changes at once.", maxRosterItems))
	}
	type change struct {
		emp   *models.Employee
		date  time.Time
		shift *uint64
		reset bool
	}
	changes := make([]change, 0, len(items))
	emps := map[uint64]*models.Employee{}
	shifts := map[uint64]bool{}
	for _, it := range items {
		emp, ok := emps[it.EmployeeID]
		if !ok {
			var e models.Employee
			if err := s.db.WithContext(ctx).First(&e, it.EmployeeID).Error; err != nil || e.IsSystemUser() {
				return 0, apperr.NotFound("EMPLOYEE_NOT_FOUND", "Employee not found.")
			}
			emp, emps[it.EmployeeID] = &e, &e
		}
		if !actor.CanManage(emp) {
			return 0, apperr.ErrForbidden
		}
		d, err := timeutil.ParseDate(it.Date)
		if err != nil {
			return 0, apperr.BadRequest("INVALID_DATE", err.Error())
		}
		chosen := 0
		for _, b := range []bool{it.WorkScheduleID != nil, it.Off, it.Reset} {
			if b {
				chosen++
			}
		}
		if chosen != 1 {
			return 0, apperr.BadRequest("INVALID_ITEM", "Each change sets exactly one of: a shift, a day off, or reset.")
		}
		if it.WorkScheduleID != nil && !shifts[*it.WorkScheduleID] {
			var n int64
			s.db.WithContext(ctx).Model(&models.WorkSchedule{}).Where("id = ?", *it.WorkScheduleID).Count(&n)
			if n == 0 {
				return 0, apperr.BadRequest("SHIFT_NOT_FOUND", "Shift not found.")
			}
			shifts[*it.WorkScheduleID] = true
		}
		changes = append(changes, change{emp: emp, date: d, shift: it.WorkScheduleID, reset: it.Reset})
	}
	by := actorRef(actor)
	err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		summary := make([]map[string]any, 0, len(changes))
		for _, c := range changes {
			what := "off"
			if c.reset {
				what = "usual"
				if err := tx.Where("employee_id = ? AND work_date = ?", c.emp.ID, c.date).Delete(&models.RosterShift{}).Error; err != nil {
					return err
				}
			} else {
				if c.shift != nil {
					what = strconv.FormatUint(*c.shift, 10)
				}
				row := models.RosterShift{EmployeeID: c.emp.ID, WorkDate: c.date, WorkScheduleID: c.shift, UpdatedBy: by}
				if err := tx.Clauses(clause.OnConflict{
					Columns:   []clause.Column{{Name: "employee_id"}, {Name: "work_date"}},
					DoUpdates: clause.AssignmentColumns([]string{"work_schedule_id", "updated_by", "updated_at"}),
				}).Omit(clause.Associations).Create(&row).Error; err != nil {
					return err
				}
			}
			summary = append(summary, map[string]any{"employee_id": c.emp.ID, "date": c.date.Format("2006-01-02"), "shift": what})
		}
		return audit.Write(ctx, tx, actor, audit.Entry{Action: audit.ActionRosterUpdate, EntityType: "roster",
			Details: map[string]any{"changes": summary}})
	})
	if err != nil {
		return 0, err
	}
	return len(changes), nil
}

// CopyRoster repeats the roster entries of the 7 days from `from` onto the 7 days
// from `to`, for the employees in scope the actor manages. Returns entries written.
func (s *Service) CopyRoster(ctx context.Context, actor *auth.Actor, from, to time.Time, f StaffFilter) (int, error) {
	if from.Equal(to) {
		return 0, apperr.BadRequest("SAME_WEEK", "Choose a different week to copy to.")
	}
	emps, err := s.staff(ctx, actor, f)
	if err != nil {
		return 0, err
	}
	var ids []uint64
	for i := range emps {
		if actor.CanManage(&emps[i]) {
			ids = append(ids, emps[i].ID)
		}
	}
	if len(ids) == 0 {
		return 0, nil
	}
	var src []models.RosterShift
	if err := s.db.WithContext(ctx).Where("employee_id IN ? AND work_date BETWEEN ? AND ?", ids, from, from.AddDate(0, 0, 6)).Find(&src).Error; err != nil {
		return 0, err
	}
	if len(src) == 0 {
		return 0, apperr.BadRequest("EMPTY_WEEK", "That week has no roster entries to copy.")
	}
	shift := to.Sub(from)
	by := actorRef(actor)
	err = s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		for _, r := range src {
			row := models.RosterShift{EmployeeID: r.EmployeeID, WorkDate: r.WorkDate.Add(shift), WorkScheduleID: r.WorkScheduleID, UpdatedBy: by}
			if err := tx.Clauses(clause.OnConflict{
				Columns:   []clause.Column{{Name: "employee_id"}, {Name: "work_date"}},
				DoUpdates: clause.AssignmentColumns([]string{"work_schedule_id", "updated_by", "updated_at"}),
			}).Omit(clause.Associations).Create(&row).Error; err != nil {
				return err
			}
		}
		return audit.Write(ctx, tx, actor, audit.Entry{Action: audit.ActionRosterCopy, EntityType: "roster",
			Details: map[string]any{"from": from.Format("2006-01-02"), "to": to.Format("2006-01-02"), "entries": len(src)}})
	})
	if err != nil {
		return 0, err
	}
	return len(src), nil
}

// ---------- special / temporary schedules ----------

type OverrideInput struct {
	EmployeeID     uint64   `json:"employee_id"`
	EmployeeIDs    []uint64 `json:"employee_ids"` // create only: the same schedule for each of these people
	StartDate      string   `json:"start_date"`
	EndDate        string   `json:"end_date"`
	Kind           string   `json:"kind"` // SHIFT | CUSTOM | OFF
	WorkScheduleID *uint64  `json:"work_schedule_id"`
	StartTime      *string  `json:"start_time"`
	EndTime        *string  `json:"end_time"`
	WorkType       *string  `json:"work_type"`
	GraceMinutes   *int     `json:"grace_minutes"`
	WorkDays       []int    `json:"work_days"`
	Reason         string   `json:"reason"`
}

type OverrideFilter struct {
	StaffFilter
	When string // active | upcoming | past | "" (all)
}

func (s *Service) Overrides(ctx context.Context, actor *auth.Actor, f OverrideFilter) ([]models.ScheduleOverride, error) {
	emps, err := s.staff(ctx, actor, f.StaffFilter)
	if err != nil {
		return nil, err
	}
	out := []models.ScheduleOverride{}
	if len(emps) == 0 {
		return out, nil
	}
	ids := make([]uint64, len(emps))
	for i, e := range emps {
		ids[i] = e.ID
	}
	st, err := s.settings.Get(ctx)
	if err != nil {
		return nil, err
	}
	today := timeutil.DateOf(s.Now(), st.Location)
	q := s.db.WithContext(ctx).Preload("Employee").Preload("Employee.Branch").Preload("WorkSchedule").Where("employee_id IN ?", ids)
	switch f.When {
	case "active":
		q = q.Where("start_date <= ? AND end_date >= ?", today, today).Order("end_date")
	case "upcoming":
		q = q.Where("start_date > ?", today).Order("start_date")
	case "past":
		q = q.Where("end_date < ?", today).Order("end_date DESC")
	default:
		q = q.Order("start_date DESC")
	}
	return out, q.Limit(500).Find(&out).Error
}

func (s *Service) CreateOverride(ctx context.Context, actor *auth.Actor, in OverrideInput) (*models.ScheduleOverride, error) {
	o := models.ScheduleOverride{}
	if err := s.applyOverride(ctx, actor, &o, in); err != nil {
		return nil, err
	}
	o.CreatedBy = actorRef(actor)
	err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := checkOverlap(tx, &o); err != nil {
			return err
		}
		if err := tx.Omit(clause.Associations).Create(&o).Error; err != nil {
			return err
		}
		return audit.Write(ctx, tx, actor, audit.Entry{Action: audit.ActionOverrideCreate, EntityType: "schedule_override", EntityID: o.ID,
			TargetEmployeeID: o.EmployeeID, Details: overrideAudit(&o)})
	})
	if err != nil {
		return nil, err
	}
	return s.override(ctx, o.ID)
}

// maxOverrideBatch caps how many people one special schedule is created for at once.
const maxOverrideBatch = 300

// CreateOverrides gives the same special schedule to each of in.EmployeeIDs (or
// in.EmployeeID): one row per person, all or nothing. Nobody may already have a
// special schedule on those dates.
func (s *Service) CreateOverrides(ctx context.Context, actor *auth.Actor, in OverrideInput) ([]models.ScheduleOverride, error) {
	ids, seen := []uint64{}, map[uint64]bool{}
	for _, id := range append(append([]uint64{}, in.EmployeeIDs...), in.EmployeeID) {
		if id != 0 && !seen[id] {
			seen[id] = true
			ids = append(ids, id)
		}
	}
	if len(ids) == 0 {
		return nil, apperr.BadRequest("EMPLOYEE_REQUIRED", "Choose at least one employee.")
	}
	if len(ids) > maxOverrideBatch {
		return nil, apperr.BadRequest("TOO_MANY_EMPLOYEES", fmt.Sprintf("Choose at most %d employees at a time.", maxOverrideBatch))
	}
	rows := make([]models.ScheduleOverride, len(ids))
	for i, id := range ids {
		one := in
		one.EmployeeID = id
		if err := s.applyOverride(ctx, actor, &rows[i], one); err != nil {
			return nil, err
		}
		rows[i].CreatedBy = actorRef(actor)
	}
	err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var clash []string
		for i := range rows {
			other, err := findOverlap(tx, &rows[i])
			if err != nil {
				return err
			}
			if other != nil {
				var e models.Employee
				tx.Select("full_name").First(&e, rows[i].EmployeeID)
				clash = append(clash, fmt.Sprintf("%s (%s – %s)", e.FullName, timeutil.FormatDate(other.StartDate), timeutil.FormatDate(other.EndDate)))
			}
		}
		if len(clash) > 0 {
			more := ""
			if len(clash) > 5 {
				clash, more = clash[:5], fmt.Sprintf(" and %d more", len(clash)-5)
			}
			return apperr.Conflict("OVERLAP", "Already on a special schedule then: "+strings.Join(clash, ", ")+more+
				". Edit those or leave them out.")
		}
		for i := range rows {
			o := &rows[i]
			if err := tx.Omit(clause.Associations).Create(o).Error; err != nil {
				return err
			}
			details := overrideAudit(o)
			if len(rows) > 1 {
				details["batch"] = len(rows)
			}
			if err := audit.Write(ctx, tx, actor, audit.Entry{Action: audit.ActionOverrideCreate, EntityType: "schedule_override", EntityID: o.ID,
				TargetEmployeeID: o.EmployeeID, Details: details}); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	created := make([]uint64, len(rows))
	for i := range rows {
		created[i] = rows[i].ID
	}
	out := []models.ScheduleOverride{}
	return out, s.db.WithContext(ctx).Preload("Employee").Preload("Employee.Branch").Preload("WorkSchedule").
		Where("id IN ?", created).Order("id").Find(&out).Error
}

func (s *Service) UpdateOverride(ctx context.Context, actor *auth.Actor, id uint64, in OverrideInput) (*models.ScheduleOverride, error) {
	var o models.ScheduleOverride
	if err := s.db.WithContext(ctx).First(&o, id).Error; err != nil {
		return nil, apperr.NotFound("OVERRIDE_NOT_FOUND", "Special schedule not found.")
	}
	before := overrideAudit(&o)
	if err := s.canManage(ctx, actor, o.EmployeeID); err != nil { // the current employee too
		return nil, err
	}
	if err := s.applyOverride(ctx, actor, &o, in); err != nil {
		return nil, err
	}
	err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := checkOverlap(tx, &o); err != nil {
			return err
		}
		if err := tx.Omit(clause.Associations).Save(&o).Error; err != nil {
			return err
		}
		return audit.Write(ctx, tx, actor, audit.Entry{Action: audit.ActionOverrideUpdate, EntityType: "schedule_override", EntityID: o.ID,
			TargetEmployeeID: o.EmployeeID, Details: map[string]any{"before": before, "after": overrideAudit(&o)}})
	})
	if err != nil {
		return nil, err
	}
	return s.override(ctx, o.ID)
}

func (s *Service) DeleteOverride(ctx context.Context, actor *auth.Actor, id uint64) error {
	var o models.ScheduleOverride
	if err := s.db.WithContext(ctx).First(&o, id).Error; err != nil {
		return apperr.NotFound("OVERRIDE_NOT_FOUND", "Special schedule not found.")
	}
	if err := s.canManage(ctx, actor, o.EmployeeID); err != nil {
		return err
	}
	return s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Delete(&o).Error; err != nil {
			return err
		}
		return audit.Write(ctx, tx, actor, audit.Entry{Action: audit.ActionOverrideDelete, EntityType: "schedule_override", EntityID: o.ID,
			TargetEmployeeID: o.EmployeeID, Details: overrideAudit(&o)})
	})
}

func (s *Service) override(ctx context.Context, id uint64) (*models.ScheduleOverride, error) {
	var o models.ScheduleOverride
	return &o, s.db.WithContext(ctx).Preload("Employee").Preload("Employee.Branch").Preload("WorkSchedule").First(&o, id).Error
}

func (s *Service) canManage(ctx context.Context, actor *auth.Actor, empID uint64) error {
	var e models.Employee
	if err := s.db.WithContext(ctx).First(&e, empID).Error; err != nil || e.IsSystemUser() { // system users have no schedule
		return apperr.NotFound("EMPLOYEE_NOT_FOUND", "Employee not found.")
	}
	if !actor.CanManage(&e) {
		return apperr.ErrForbidden
	}
	return nil
}

func (s *Service) applyOverride(ctx context.Context, actor *auth.Actor, o *models.ScheduleOverride, in OverrideInput) error {
	if err := s.canManage(ctx, actor, in.EmployeeID); err != nil {
		return err
	}
	start, err := timeutil.ParseDate(in.StartDate)
	if err != nil {
		return apperr.BadRequest("INVALID_DATE", "Start date: "+err.Error())
	}
	end, err := timeutil.ParseDate(in.EndDate)
	if err != nil {
		return apperr.BadRequest("INVALID_DATE", "End date: "+err.Error())
	}
	if end.Before(start) {
		return apperr.BadRequest("INVALID_RANGE", "End date must be on or after start date.")
	}
	if end.Sub(start) >= maxOverrideDays*24*time.Hour {
		return apperr.BadRequest("RANGE_TOO_LONG", "A special schedule can last at most one year. For a permanent change, set the employee's usual shift.")
	}
	reason := strings.TrimSpace(in.Reason)
	if len([]rune(reason)) > 255 {
		return apperr.BadRequest("REASON_TOO_LONG", "Reason must be at most 255 characters.")
	}
	*o = models.ScheduleOverride{ID: o.ID, CreatedBy: o.CreatedBy, CreatedAt: o.CreatedAt, EmployeeID: in.EmployeeID,
		StartDate: start, EndDate: end, Kind: strings.ToUpper(strings.TrimSpace(in.Kind)), Reason: reason}
	switch o.Kind {
	case models.OverrideOff:
		o.WorkDays = "1,2,3,4,5,6,7"
	case models.OverrideShift:
		if in.WorkScheduleID == nil {
			return apperr.BadRequest("SHIFT_REQUIRED", "Choose the shift to work.")
		}
		var ws models.WorkSchedule
		if err := s.db.WithContext(ctx).First(&ws, *in.WorkScheduleID).Error; err != nil {
			return apperr.BadRequest("SHIFT_NOT_FOUND", "Shift not found.")
		}
		o.WorkScheduleID = &ws.ID
		if o.WorkDays, err = workDays(in.WorkDays, true); err != nil {
			return err
		}
		if o.WorkDays == "" {
			o.WorkDays = ws.WorkDays // the shift's own days
		}
	case models.OverrideCustom:
		if in.StartTime == nil || in.EndTime == nil {
			return apperr.BadRequest("TIMES_REQUIRED", "Start and end times are required for custom hours.")
		}
		wt := models.WorkNormal
		if in.WorkType != nil {
			wt = strings.ToUpper(strings.TrimSpace(*in.WorkType))
		}
		st, et := strings.TrimSpace(*in.StartTime), strings.TrimSpace(*in.EndTime)
		if err := checkHours(st, et, wt); err != nil {
			return err
		}
		grace := 0
		if in.GraceMinutes != nil {
			grace = *in.GraceMinutes
		}
		if grace < 0 || grace > maxGrace {
			return apperr.BadRequest("INVALID_GRACE", fmt.Sprintf("Grace period must be 0–%d minutes.", maxGrace))
		}
		o.StartTime, o.EndTime, o.WorkType, o.GraceMinutes = &st, &et, &wt, &grace
		if o.WorkDays, err = workDays(in.WorkDays, false); err != nil {
			return err
		}
	default:
		return apperr.BadRequest("INVALID_KIND", "Kind must be SHIFT, CUSTOM or OFF.")
	}
	return nil
}

// findOverlap returns another special schedule of the same person on o's dates.
func findOverlap(tx *gorm.DB, o *models.ScheduleOverride) (*models.ScheduleOverride, error) {
	var other models.ScheduleOverride
	err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).
		Where("employee_id = ? AND id <> ? AND start_date <= ? AND end_date >= ?", o.EmployeeID, o.ID, o.EndDate, o.StartDate).
		First(&other).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &other, nil
}

func checkOverlap(tx *gorm.DB, o *models.ScheduleOverride) error {
	other, err := findOverlap(tx, o)
	if err != nil || other == nil {
		return err
	}
	return apperr.Conflict("OVERLAP", fmt.Sprintf("This employee already has a special schedule from %s to %s. Edit that one or pick other dates.",
		timeutil.FormatDate(other.StartDate), timeutil.FormatDate(other.EndDate)))
}

func overrideAudit(o *models.ScheduleOverride) map[string]any {
	m := map[string]any{"employee_id": o.EmployeeID, "from": o.StartDate.Format("2006-01-02"), "to": o.EndDate.Format("2006-01-02"),
		"kind": o.Kind, "work_days": o.WorkDays, "reason": o.Reason}
	if o.WorkScheduleID != nil {
		m["shift_id"] = *o.WorkScheduleID
	}
	if o.StartTime != nil && o.EndTime != nil {
		m["hours"] = *o.StartTime + "-" + *o.EndTime
		m["work_type"] = deref(o.WorkType)
	}
	return m
}

// actorRef is who to record as author: nobody for the command line / system (id 0).
func actorRef(a *auth.Actor) *uint64 {
	if a == nil || a.ID() == 0 {
		return nil
	}
	id := a.ID()
	return &id
}

func deref(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}

// weekdayNames is used by exports ("Mon 20 Oct").
var weekdayNames = []string{"Mon", "Tue", "Wed", "Thu", "Fri", "Sat", "Sun"}

func dayLabel(d time.Time) string {
	return fmt.Sprintf("%s %s", weekdayNames[timeutil.ISOWeekday(d)-1], d.Format("02 Jan"))
}

// cellText is how a roster cell reads in exports.
func cellText(c RosterCell) string {
	switch {
	case c.Leave != "":
		return "Leave: " + c.Leave
	case c.Holiday != "" && c.Work:
		return "Holiday: " + c.Holiday
	case !c.Work:
		if c.Source == attendance.PlanSpecial && c.Note != "" {
			return "Off (" + c.Note + ")"
		}
		return "Off"
	}
	t := fmt.Sprintf("%s %s–%s", c.ShiftName, c.Start, c.End)
	if c.CrossDay {
		t += " (+1)"
	}
	if c.Source == attendance.PlanSpecial {
		t += " *"
	}
	return t
}
