// Package report builds attendance reports for any employee scope and date range,
// including days with no record (absent) derived from schedules, holidays and leave.
package report

import (
	"context"
	"fmt"
	"sort"
	"time"

	"attendance/backend/internal/apperr"
	"attendance/backend/internal/attendance"
	"attendance/backend/internal/auth"
	"attendance/backend/internal/models"
	"attendance/backend/internal/settings"
	"attendance/backend/internal/timeutil"

	"gorm.io/gorm"
)

// Effective day statuses in reports (attendance statuses plus the not-yet state for today).
const (
	DayNotYet = "NOT_CHECKED_IN" // a working day that is today (or later) with no check-in
)

const maxRangeDays = 366

type Service struct {
	db       *gorm.DB
	settings *settings.Service
	Now      func() time.Time
}

func NewService(db *gorm.DB, st *settings.Service) *Service {
	return &Service{db: db, settings: st, Now: func() time.Time { return time.Now().UTC() }}
}

type DayRow struct {
	AttendanceID   uint64     `json:"attendance_id,omitempty"`
	EmployeeID     uint64     `json:"employee_id"`
	EmployeeCode   string     `json:"employee_code"`
	FullName       string     `json:"full_name"`
	Department     string     `json:"department"`
	BranchID       uint64     `json:"branch_id,omitempty"`
	Branch         string     `json:"branch"`
	Date           time.Time  `json:"date"`
	CheckInAt      *time.Time `json:"check_in_at"`
	CheckOutAt     *time.Time `json:"check_out_at"`
	WorkingMinutes int        `json:"working_minutes"`
	LateMinutes    int        `json:"late_minutes"`
	EarlyMinutes   int        `json:"early_leave_minutes"`
	Status         string     `json:"status"`
	Confirmation   string     `json:"confirmation_status"`
	ConfirmedBy    string     `json:"confirmed_by"`
	LeaveType      string     `json:"leave_type"`
	LeaveHalf      string     `json:"leave_half,omitempty"` // FIRST_HALF | SECOND_HALF: half the day was leave
	// The day's scheduled shift (empty on days off).
	Shift      string `json:"shift,omitempty"`
	ShiftStart string `json:"shift_start,omitempty"`
	ShiftEnd   string `json:"shift_end,omitempty"`
	CrossDay   bool   `json:"cross_day,omitempty"`
	ShiftGrace int    `json:"-"`
}

type Summary struct {
	EmployeeID      uint64  `json:"employee_id"`
	EmployeeCode    string  `json:"employee_code"`
	FullName        string  `json:"full_name"`
	Department      string  `json:"department"`
	Branch          string  `json:"branch"`
	WorkingDays     int     `json:"working_days"` // expected (scheduled, non-holiday) days so far
	PresentDays     int     `json:"present_days"`
	LateDays        int     `json:"late_days"`
	EarlyLeaveDays  int     `json:"early_leave_days"`
	AbsentDays      int     `json:"absent_days"`
	LeaveDays       float64 `json:"leave_days"` // half days count 0.5
	PendingDays     int     `json:"pending_days"`
	RejectedDays    int     `json:"rejected_days"`
	MissingCheckout int     `json:"missing_checkout"`
	LateMinutes     int     `json:"late_minutes"`
	WorkingMinutes  int     `json:"working_minutes"`
	AvgMinutes      int     `json:"avg_minutes_per_day"`
}

type Report struct {
	From       time.Time      `json:"from"`
	To         time.Time      `json:"to"`
	Today      time.Time      `json:"today"`
	Rows       []DayRow       `json:"rows"`
	Summaries  []Summary      `json:"summaries"`
	Totals     Summary        `json:"totals"`
	StaffCount int            `json:"staff_count"`
	Loc        *time.Location `json:"-"`
	Company    string         `json:"company"`
}

type Filter struct {
	EmployeeIDs  []uint64 // optional subset (still intersected with the actor's scope)
	DepartmentID uint64
	BranchID     uint64
	From, To     time.Time
}

// Build produces day rows + per-employee summaries for everything the actor may see.
func (s *Service) Build(ctx context.Context, actor *auth.Actor, f Filter) (*Report, error) {
	if f.To.Before(f.From) {
		return nil, apperr.BadRequest("INVALID_RANGE", "End date must be on or after start date.")
	}
	if f.To.Sub(f.From) > maxRangeDays*24*time.Hour {
		return nil, apperr.BadRequest("RANGE_TOO_LONG", "Reports are limited to one year.")
	}
	st, err := s.settings.Get(ctx)
	if err != nil {
		return nil, err
	}
	scope, err := auth.Scope(ctx, s.db, actor)
	if err != nil {
		return nil, err
	}
	today := timeutil.DateOf(s.Now(), st.Location)

	// employees
	q := s.db.WithContext(ctx).Preload("Department").Preload("Branch").Preload("WorkSchedule").
		Where("kind = ?", models.KindStaff). // system users are not staff
		Where("deleted_at IS NULL OR deleted_at >= ?", f.From).Order("full_name")
	if scope != nil {
		q = q.Where("id IN ?", scope)
	}
	if len(f.EmployeeIDs) > 0 {
		q = q.Where("id IN ?", f.EmployeeIDs)
	}
	if f.DepartmentID != 0 {
		q = q.Where("department_id = ?", f.DepartmentID)
	}
	if f.BranchID != 0 {
		q = q.Where("branch_id = ?", f.BranchID)
	}
	var emps []models.Employee
	if err := q.Find(&emps).Error; err != nil {
		return nil, err
	}
	// Empty slices, not nil: clients get [] rather than null when nothing matches.
	rep := &Report{From: f.From, To: f.To, Today: today, Loc: st.Location, Company: st.CompanyName, Rows: []DayRow{}, Summaries: []Summary{}}
	if len(emps) == 0 {
		return rep, nil
	}
	ids := make([]uint64, len(emps))
	for i, e := range emps {
		ids[i] = e.ID
	}

	// attendance rows (+ confirmer name)
	type attRow struct {
		models.Attendance
		ConfirmerName string
	}
	var atts []attRow
	if err := s.db.WithContext(ctx).Table("attendance a").
		Select("a.*, c.full_name AS confirmer_name").
		Joins("LEFT JOIN employees c ON c.id = a.confirmed_by").
		Where("a.employee_id IN ? AND a.work_date BETWEEN ? AND ?", ids, f.From, f.To).
		Scan(&atts).Error; err != nil {
		return nil, err
	}
	byKey := map[string]attRow{}
	for _, a := range atts {
		byKey[key(a.EmployeeID, a.WorkDate)] = a
	}

	// approved leave overlapping range
	var leaves []models.LeaveRequest
	if err := s.db.WithContext(ctx).Preload("LeaveType").
		Where("employee_id IN ? AND status = ? AND start_date <= ? AND end_date >= ?", ids, models.LeaveApproved, f.To, f.From).
		Find(&leaves).Error; err != nil {
		return nil, err
	}
	leaveName := func(l *models.LeaveRequest) string {
		if l.LeaveType != nil {
			return l.LeaveType.Name
		}
		return "Leave"
	}
	// leaveOn: a full day's leave; halfOn: a half day's ("Annual Leave (first half)").
	leaveOn := func(empID uint64, d time.Time) string {
		for i := range leaves {
			if l := &leaves[i]; l.EmployeeID == empID && !l.IsHalfDay() && !d.Before(l.StartDate) && !d.After(l.EndDate) {
				return leaveName(l)
			}
		}
		return ""
	}
	halfOn := func(empID uint64, d time.Time) (string, string) {
		for i := range leaves {
			if l := &leaves[i]; l.EmployeeID == empID && l.IsHalfDay() && d.Equal(l.StartDate) {
				part := "first half"
				if l.DayType == models.LeaveSecondHalf {
					part = "second half"
				}
				return fmt.Sprintf("%s (%s)", leaveName(l), part), l.DayType
			}
		}
		return "", ""
	}
	isHol, err := attendance.HolidaySet(ctx, s.db, f.From, f.To)
	if err != nil {
		return nil, err
	}

	plan, err := attendance.LoadPlanner(ctx, s.db, ids, f.From, f.To)
	if err != nil {
		return nil, err
	}

	for i := range emps {
		e := &emps[i]
		dept, branch := "", ""
		var branchID uint64
		if e.Department != nil {
			dept = e.Department.Name
		}
		if e.Branch != nil {
			branch, branchID = e.Branch.Name, e.Branch.ID
		}
		sum := Summary{EmployeeID: e.ID, EmployeeCode: e.EmployeeCode, FullName: e.FullName, Department: dept, Branch: branch}
		// Days before the employee joined — or before they were registered in this
		// system — are never "absent". Recorded rows are always shown.
		startDate := timeutil.DateOf(e.CreatedAt, st.Location)
		if e.JoinDate != nil && e.JoinDate.After(startDate) {
			startDate = *e.JoinDate
		}
		for d := f.From; !d.After(f.To); d = d.AddDate(0, 0, 1) {
			if e.DeletedAt != nil && timeutil.DateOf(*e.DeletedAt, st.Location).Before(d) {
				continue
			}
			row := DayRow{EmployeeID: e.ID, EmployeeCode: e.EmployeeCode, FullName: e.FullName, Department: dept, BranchID: branchID, Branch: branch, Date: d}
			day := plan.Day(e, d)
			expected := day.Work && !isHol(d)
			if day.Work {
				row.Shift, row.ShiftStart, row.ShiftEnd, row.CrossDay, row.ShiftGrace = day.ShiftName, day.Start, day.End, day.CrossDay, day.Grace
			}
			if a, ok := byKey[key(e.ID, d)]; ok {
				row.AttendanceID = a.ID
				row.CheckInAt, row.CheckOutAt = a.CheckInAt, a.CheckOutAt
				row.WorkingMinutes, row.LateMinutes, row.EarlyMinutes = a.WorkingMinutes, a.LateMinutes, a.EarlyLeaveMinutes
				row.Status, row.Confirmation, row.ConfirmedBy = a.Status, a.ConfirmationStatus, a.ConfirmerName
				if a.Status == models.StatusLeave {
					row.LeaveType = leaveOn(e.ID, d)
				}
			} else {
				if !expected || d.After(today) || d.Before(startDate) {
					continue // weekends, holidays and future days are never "absent"
				}
				if lt := leaveOn(e.ID, d); lt != "" {
					row.Status, row.LeaveType, row.Confirmation = models.StatusLeave, lt, models.ConfConfirmed
				} else if d.Equal(today) {
					row.Status = DayNotYet
				} else {
					row.Status = models.StatusAbsent
				}
			}
			if row.LeaveType == "" && expected {
				row.LeaveType, row.LeaveHalf = halfOn(e.ID, d)
			}
			if expected && !d.After(today) && !d.Before(startDate) {
				sum.WorkingDays++
			}
			accumulate(&sum, row)
			rep.Rows = append(rep.Rows, row)
		}
		if sum.PresentDays > 0 {
			withOut := sum.PresentDays - sum.MissingCheckout
			if withOut > 0 {
				sum.AvgMinutes = sum.WorkingMinutes / withOut
			}
		}
		rep.Summaries = append(rep.Summaries, sum)
		addTotals(&rep.Totals, sum)
	}
	rep.StaffCount = len(emps)
	sort.SliceStable(rep.Rows, func(i, j int) bool {
		if !rep.Rows[i].Date.Equal(rep.Rows[j].Date) {
			return rep.Rows[i].Date.Before(rep.Rows[j].Date)
		}
		return rep.Rows[i].FullName < rep.Rows[j].FullName
	})
	if rep.Totals.PresentDays-rep.Totals.MissingCheckout > 0 {
		rep.Totals.AvgMinutes = rep.Totals.WorkingMinutes / (rep.Totals.PresentDays - rep.Totals.MissingCheckout)
	}
	return rep, nil
}

func accumulate(s *Summary, r DayRow) {
	if r.CheckInAt != nil {
		s.PresentDays++
		s.WorkingMinutes += r.WorkingMinutes
		if r.CheckOutAt == nil {
			s.MissingCheckout++
		}
	}
	if r.LateMinutes > 0 {
		s.LateDays++
		s.LateMinutes += r.LateMinutes
	}
	if r.EarlyMinutes > 0 {
		s.EarlyLeaveDays++
	}
	switch r.Status {
	case models.StatusAbsent:
		s.AbsentDays++
	case models.StatusLeave:
		s.LeaveDays++
	}
	if r.LeaveHalf != "" {
		s.LeaveDays += 0.5
	}
	switch r.Confirmation {
	case models.ConfPending:
		s.PendingDays++
	case models.ConfRejected:
		s.RejectedDays++
	}
}

func addTotals(t *Summary, s Summary) {
	t.WorkingDays += s.WorkingDays
	t.PresentDays += s.PresentDays
	t.LateDays += s.LateDays
	t.EarlyLeaveDays += s.EarlyLeaveDays
	t.AbsentDays += s.AbsentDays
	t.LeaveDays += s.LeaveDays
	t.PendingDays += s.PendingDays
	t.RejectedDays += s.RejectedDays
	t.MissingCheckout += s.MissingCheckout
	t.LateMinutes += s.LateMinutes
	t.WorkingMinutes += s.WorkingMinutes
}

func key(id uint64, d time.Time) string { return d.Format("2006-01-02") + "#" + itoa(id) }

func itoa(v uint64) string {
	if v == 0 {
		return "0"
	}
	var b [20]byte
	i := len(b)
	for v > 0 {
		i--
		b[i] = byte('0' + v%10)
		v /= 10
	}
	return string(b[i:])
}

// Today returns the calendar date "now" in the company timezone.
func (s *Service) Today(ctx context.Context) (time.Time, *time.Location) {
	loc := s.settings.Location(ctx)
	return timeutil.DateOf(s.Now(), loc), loc
}
