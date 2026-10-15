// Package report builds attendance reports for any employee scope and date range,
// including days with no record (absent) derived from schedules, holidays and leave.
package report

import (
	"context"
	"fmt"
	"sort"
	"strings"
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

// Place is where someone checked in or out, and how far that was from the office.
type Place struct {
	Latitude       float64  `json:"latitude"`
	Longitude      float64  `json:"longitude"`
	DistanceMeters *float64 `json:"distance_meters"`
	InsideOffice   *bool    `json:"inside_office"`
}

type DayRow struct {
	AttendanceID           uint64     `json:"attendance_id,omitempty"`
	EmployeeID             uint64     `json:"employee_id"`
	EmployeeCode           string     `json:"employee_code"`
	FullName               string     `json:"full_name"`
	Department             string     `json:"department"`
	BranchID               uint64     `json:"branch_id,omitempty"`
	Branch                 string     `json:"branch"`
	Date                   time.Time  `json:"date"`
	CheckInAt              *time.Time `json:"check_in_at"`
	CheckOutAt             *time.Time `json:"check_out_at"`
	CheckInReason          string     `json:"check_in_reason,omitempty"`
	CheckOutReason         string     `json:"check_out_reason,omitempty"`
	OvertimeMinutes        int        `json:"overtime_minutes"`         // approved overtime on that date
	OvertimePendingMinutes int        `json:"overtime_pending_minutes"` // asked for, not yet reviewed
	CheckInPlace           *Place     `json:"check_in_place,omitempty"` // where they were (when shared)
	CheckOutPlace          *Place     `json:"check_out_place,omitempty"`
	WorkingMinutes         int        `json:"working_minutes"`
	LateMinutes            int        `json:"late_minutes"`
	EarlyMinutes           int        `json:"early_leave_minutes"`
	Status                 string     `json:"status"`
	Confirmation           string     `json:"confirmation_status"`
	ConfirmedBy            string     `json:"confirmed_by"`
	LeaveType              string     `json:"leave_type"`
	LeaveHalf              string     `json:"leave_half,omitempty"` // FIRST_HALF | SECOND_HALF: half the day was leave
	LeaveCode              string     `json:"-"`                    // the leave type's code (ANNUAL = annual leave)
	AfterShiftMinutes      int        `json:"-"`                    // checked out this long after the shift's end
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
	AbsentDays      float64 `json:"absent_days"` // absent for half a day (the other half leave) counts 0.5
	LeaveDays       float64 `json:"leave_days"`  // half days count 0.5
	PendingDays     int     `json:"pending_days"`
	RejectedDays    int     `json:"rejected_days"`
	MissingCheckout int     `json:"missing_checkout"`
	LateMinutes     int     `json:"late_minutes"`
	WorkingMinutes  int     `json:"working_minutes"`
	AvgMinutes      int     `json:"avg_minutes_per_day"`
	// Overtime in the period (by the date it belongs to): approved, and still pending.
	OvertimeMinutes        int `json:"overtime_minutes"`
	OvertimePendingMinutes int `json:"overtime_pending_minutes"`
}

// OvertimeRow is one overtime request in the period (pending or approved), for exports.
type OvertimeRow struct {
	EmployeeID                                 uint64
	EmployeeCode, FullName, Branch, Department string
	Date                                       time.Time
	StartAt, EndAt                             time.Time
	Minutes                                    int
	Reason, Status, ReviewedBy                 string
	ReviewedAt                                 *time.Time
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
	// For exports: who made it and when, the filters in words, and the overtime requests.
	GeneratedBy string        `json:"-"`
	GeneratedAt time.Time     `json:"-"`
	Scope       string        `json:"-"` // "Branch: Main branch · Department: Sales"
	Overtime    []OvertimeRow `json:"-"`
	// Each person's days off and holidays in the period, worked or not: key(employee,
	// date) → the holiday's name, or "" for a day off. For the payroll grid.
	OffDays map[string]string `json:"-"`
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
	rep := &Report{From: f.From, To: f.To, Today: today, Loc: st.Location, Company: st.CompanyName, Rows: []DayRow{}, Summaries: []Summary{}, OffDays: map[string]string{},
		GeneratedBy: actor.Employee.FullName, GeneratedAt: s.Now(), Scope: s.scopeText(ctx, f)}
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
	attIDs := make([]uint64, 0, len(atts))
	for _, a := range atts {
		byKey[key(a.EmployeeID, a.WorkDate)] = a
		attIDs = append(attIDs, a.ID)
	}
	// The latest position of each check-in and check-out (a re-submission adds one).
	places := map[string]*Place{}
	if len(attIDs) > 0 {
		var locs []models.AttendanceLocation
		if err := s.db.WithContext(ctx).Where("attendance_id IN ?", attIDs).Order("id").Find(&locs).Error; err != nil {
			return nil, err
		}
		for _, l := range locs {
			places[fmt.Sprintf("%d|%s", l.AttendanceID, l.Type)] = &Place{Latitude: l.Latitude, Longitude: l.Longitude, DistanceMeters: l.DistanceMeters, InsideOffice: l.InsideOffice}
		}
	}

	// overtime by person and date: approved and pending minutes
	type otSum struct {
		EmployeeID uint64
		WorkDate   time.Time
		Status     string
		Minutes    int
	}
	var ots []otSum
	if err := s.db.WithContext(ctx).Model(&models.OvertimeRequest{}).
		Select("employee_id, work_date, status, SUM(minutes) AS minutes").
		Where("employee_id IN ? AND work_date BETWEEN ? AND ? AND status IN ?", ids, f.From, f.To, []string{models.LeaveApproved, models.LeavePending}).
		Group("employee_id, work_date, status").Scan(&ots).Error; err != nil {
		return nil, err
	}
	var otReqs []models.OvertimeRequest
	if err := s.db.WithContext(ctx).Preload("Employee").Preload("Employee.Branch").Preload("Employee.Department").Preload("Reviewer").
		Where("employee_id IN ? AND work_date BETWEEN ? AND ? AND status IN ?", ids, f.From, f.To, []string{models.LeaveApproved, models.LeavePending}).
		Order("work_date, start_at").Find(&otReqs).Error; err != nil {
		return nil, err
	}
	for _, o := range otReqs {
		row := OvertimeRow{EmployeeID: o.EmployeeID, Date: o.WorkDate, StartAt: o.StartAt, EndAt: o.EndAt, Minutes: o.Minutes, Reason: o.Reason, Status: o.Status, ReviewedAt: o.ReviewedAt}
		if e := o.Employee; e != nil {
			row.EmployeeCode, row.FullName = e.EmployeeCode, e.FullName
			if e.Branch != nil {
				row.Branch = e.Branch.Name
			}
			if e.Department != nil {
				row.Department = e.Department.Name
			}
		}
		if o.Reviewer != nil {
			row.ReviewedBy = o.Reviewer.FullName
		}
		rep.Overtime = append(rep.Overtime, row)
	}
	otApproved, otPending := map[string]int{}, map[string]int{}
	otEmpApproved, otEmpPending := map[uint64]int{}, map[uint64]int{}
	for _, o := range ots {
		if o.Status == models.LeaveApproved {
			otApproved[key(o.EmployeeID, o.WorkDate)] += o.Minutes
			otEmpApproved[o.EmployeeID] += o.Minutes
		} else {
			otPending[key(o.EmployeeID, o.WorkDate)] += o.Minutes
			otEmpPending[o.EmployeeID] += o.Minutes
		}
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
	leaveCode := func(l *models.LeaveRequest) string {
		if l.LeaveType != nil {
			return l.LeaveType.Code
		}
		return ""
	}
	// leaveOn: a full day's leave (name, code); halfOn: a half day's ("Annual Leave (first half)", half, code).
	leaveOn := func(empID uint64, d time.Time) (string, string) {
		for i := range leaves {
			if l := &leaves[i]; l.EmployeeID == empID && !l.IsHalfDay() && !d.Before(l.StartDate) && !d.After(l.EndDate) {
				return leaveName(l), leaveCode(l)
			}
		}
		return "", ""
	}
	halfOn := func(empID uint64, d time.Time) (string, string, string) {
		for i := range leaves {
			if l := &leaves[i]; l.EmployeeID == empID && l.IsHalfDay() && d.Equal(l.StartDate) {
				part := "first half"
				if l.DayType == models.LeaveSecondHalf {
					part = "second half"
				}
				return fmt.Sprintf("%s (%s)", leaveName(l), part), l.DayType, leaveCode(l)
			}
		}
		return "", "", ""
	}
	// Each branch has its own holidays.
	branchSet := map[uint64]bool{}
	for _, e := range emps {
		if e.BranchID != nil {
			branchSet[*e.BranchID] = true
		}
	}
	branchIDs := make([]uint64, 0, len(branchSet))
	for id := range branchSet {
		branchIDs = append(branchIDs, id)
	}
	holidays, err := attendance.Holidays(ctx, s.db, branchIDs, f.From, f.To)
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
		isHol := func(d time.Time) bool { _, ok := holidays[branchID][d.Format("2006-01-02")]; return ok }
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
			if !expected && !d.Before(startDate) {
				rep.OffDays[key(e.ID, d)] = holidays[branchID][d.Format("2006-01-02")]
			}
			if day.Work {
				row.Shift, row.ShiftStart, row.ShiftEnd, row.CrossDay, row.ShiftGrace = day.ShiftName, day.Start, day.End, day.CrossDay, day.Grace
			}
			if a, ok := byKey[key(e.ID, d)]; ok {
				row.AttendanceID = a.ID
				row.CheckInAt, row.CheckOutAt = a.CheckInAt, a.CheckOutAt
				row.CheckInReason, row.CheckOutReason = a.CheckInReason, a.CheckOutReason
				row.CheckInPlace = places[fmt.Sprintf("%d|%s", a.ID, models.TypeCheckIn)]
				if a.CheckOutAt != nil {
					row.CheckOutPlace = places[fmt.Sprintf("%d|%s", a.ID, models.TypeCheckOut)]
				}
				row.WorkingMinutes, row.LateMinutes, row.EarlyMinutes = a.WorkingMinutes, a.LateMinutes, a.EarlyLeaveMinutes
				row.Status, row.Confirmation, row.ConfirmedBy = a.Status, a.ConfirmationStatus, a.ConfirmerName
				if a.Status == models.StatusLeave {
					row.LeaveType, row.LeaveCode = leaveOn(e.ID, d)
				}
				if a.CheckOutAt != nil && expected {
					if end, err := day.EndAt(d, st.Location); err == nil && a.CheckOutAt.After(end) {
						row.AfterShiftMinutes = int(a.CheckOutAt.Sub(end) / time.Minute)
					}
				}
			} else {
				if !expected || d.After(today) || d.Before(startDate) {
					continue // weekends, holidays and future days are never "absent"
				}
				if lt, code := leaveOn(e.ID, d); lt != "" {
					row.Status, row.LeaveType, row.LeaveCode, row.Confirmation = models.StatusLeave, lt, code, models.ConfConfirmed
				} else if d.Equal(today) {
					row.Status = DayNotYet
				} else {
					row.Status = models.StatusAbsent
				}
			}
			if row.LeaveType == "" && expected {
				row.LeaveType, row.LeaveHalf, row.LeaveCode = halfOn(e.ID, d)
			}
			if expected && !d.After(today) && !d.Before(startDate) {
				sum.WorkingDays++
			}
			row.OvertimeMinutes, row.OvertimePendingMinutes = otApproved[key(e.ID, d)], otPending[key(e.ID, d)]
			accumulate(&sum, row)
			rep.Rows = append(rep.Rows, row)
		}
		// every overtime day counts, also one with no attendance row (a day off)
		sum.OvertimeMinutes, sum.OvertimePendingMinutes = otEmpApproved[e.ID], otEmpPending[e.ID]
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

// scopeText: the report's filters in words ("All branches", "Branch: Main branch · Department: Sales").
func (s *Service) scopeText(ctx context.Context, f Filter) string {
	var parts []string
	name := func(table string, id uint64) string {
		var n string
		s.db.WithContext(ctx).Table(table).Select("name").Where("id = ?", id).Scan(&n)
		return n
	}
	if f.BranchID != 0 {
		parts = append(parts, "Branch: "+name("branches", f.BranchID))
	}
	if f.DepartmentID != 0 {
		parts = append(parts, "Department: "+name("departments", f.DepartmentID))
	}
	if len(f.EmployeeIDs) == 1 {
		var n string
		s.db.WithContext(ctx).Table("employees").Select("full_name").Where("id = ?", f.EmployeeIDs[0]).Scan(&n)
		parts = append(parts, "Employee: "+n)
	}
	if len(parts) == 0 {
		return "Everyone you can see"
	}
	return strings.Join(parts, " · ")
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
		if r.LeaveHalf != "" {
			s.AbsentDays += 0.5 // the other half was leave
		} else {
			s.AbsentDays++
		}
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
	t.OvertimeMinutes += s.OvertimeMinutes
	t.OvertimePendingMinutes += s.OvertimePendingMinutes
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
