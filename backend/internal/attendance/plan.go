package attendance

import (
	"context"
	"fmt"
	"math"
	"strconv"
	"strings"
	"time"

	"attendance/backend/internal/models"
	"attendance/backend/internal/timeutil"

	"gorm.io/gorm"
)

// Day is what an employee is scheduled to work on one date: a special schedule
// covering the date wins, then the weekly roster's entry for the date, then the
// employee's usual shift and its work days. Holidays are applied by callers.
type Day struct {
	Work       bool    `json:"work"`
	Start      string  `json:"start,omitempty"` // HH:MM company time
	End        string  `json:"end,omitempty"`   // HH:MM; on the next day when CrossDay
	Grace      int     `json:"grace_minutes"`
	CrossDay   bool    `json:"cross_day"`
	StartNext  bool    `json:"start_next_day,omitempty"` // starts after midnight (second half of a night shift)
	ShiftID    *uint64 `json:"shift_id,omitempty"`
	ShiftName  string  `json:"shift_name,omitempty"`
	Color      string  `json:"color,omitempty"`
	Source     string  `json:"source"` // PlanDefault | PlanRoster | PlanSpecial
	OverrideID *uint64 `json:"override_id,omitempty"`
	Note       string  `json:"note,omitempty"` // the special schedule's reason
}

// Where a Day comes from.
const (
	PlanDefault = "DEFAULT" // the employee's usual shift and work days
	PlanRoster  = "ROSTER"  // set for this date on the weekly roster
	PlanSpecial = "SPECIAL" // a special / temporary schedule
)

// DefaultShift is used when the company has no default shift in the database.
func DefaultShift() models.WorkSchedule {
	return models.WorkSchedule{Name: "Office Hours", StartTime: "09:00", EndTime: "17:30", WorkType: models.WorkNormal,
		GraceMinutes: 10, WorkDays: "1,2,3,4,5", Color: "#2563eb"}
}

// StartAt is the shift's start on date (or the next day for StartNext).
func (d Day) StartAt(date time.Time, loc *time.Location) (time.Time, error) {
	if d.StartNext {
		date = date.AddDate(0, 0, 1)
	}
	return timeutil.At(date, d.Start, loc)
}

// EndAt is the shift's end: on date, or on the next day for a cross-day shift.
func (d Day) EndAt(date time.Time, loc *time.Location) (time.Time, error) {
	if d.CrossDay {
		date = date.AddDate(0, 0, 1)
	}
	return timeutil.At(date, d.End, loc)
}

// LateMinutes: an employee is late only after start+grace, but once late the
// minutes are counted from the scheduled start (09:15 with 10 min grace = 15 min late).
func (d Day) LateMinutes(date, checkIn time.Time, loc *time.Location) int {
	start, err := d.StartAt(date, loc)
	if err != nil || !checkIn.After(start.Add(time.Duration(d.Grace)*time.Minute)) {
		return 0
	}
	return int(checkIn.Sub(start) / time.Minute)
}

// EarlyLeaveMinutes: minutes between check-out and the scheduled end (0 if on/after it).
func (d Day) EarlyLeaveMinutes(date, checkOut time.Time, loc *time.Location) int {
	end, err := d.EndAt(date, loc)
	if err != nil || !checkOut.Before(end) {
		return 0
	}
	return int(math.Ceil(end.Sub(checkOut).Minutes()))
}

// ShiftDay is a shift as worked on a date (callers decide whether it is a work day).
func ShiftDay(ws *models.WorkSchedule) Day {
	id := ws.ID
	d := Day{Work: true, Start: ws.StartTime, End: ws.EndTime, Grace: ws.GraceMinutes, CrossDay: ws.WorkType == models.WorkCrossDay,
		ShiftName: ws.Name, Color: ws.Color}
	if id != 0 {
		d.ShiftID = &id
	}
	return d
}

// ParseWorkDays reads "1,2,3,4,5" (ISO weekdays, 1 = Monday) into a set.
func ParseWorkDays(csv string) map[int]bool {
	set := map[int]bool{}
	for _, p := range strings.Split(csv, ",") {
		if d, err := strconv.Atoi(strings.TrimSpace(p)); err == nil && d >= 1 && d <= 7 {
			set[d] = true
		}
	}
	return set
}

// Planner answers Day() for a set of employees over a date range from a few queries.
type Planner struct {
	shifts    map[uint64]*models.WorkSchedule
	fallback  *models.WorkSchedule
	roster    map[string]*uint64 // employee|date -> shift (nil = day off); no key = not on the roster
	overrides map[uint64][]models.ScheduleOverride
}

func planKey(empID uint64, d time.Time) string {
	return fmt.Sprintf("%d|%s", empID, d.Format("2006-01-02"))
}

// LoadPlanner reads shifts, roster entries and special schedules for empIDs in [from, to].
func LoadPlanner(ctx context.Context, db *gorm.DB, empIDs []uint64, from, to time.Time) (*Planner, error) {
	p := &Planner{shifts: map[uint64]*models.WorkSchedule{}, roster: map[string]*uint64{}, overrides: map[uint64][]models.ScheduleOverride{}}
	var shifts []models.WorkSchedule
	if err := db.WithContext(ctx).Order("id").Find(&shifts).Error; err != nil {
		return nil, err
	}
	for i := range shifts {
		s := &shifts[i]
		p.shifts[s.ID] = s
		if s.IsDefault && p.fallback == nil {
			p.fallback = s
		}
	}
	if p.fallback == nil {
		d := DefaultShift()
		p.fallback = &d
	}
	if len(empIDs) == 0 {
		return p, nil
	}
	var rs []models.RosterShift
	if err := db.WithContext(ctx).Where("employee_id IN ? AND work_date BETWEEN ? AND ?", empIDs, from, to).Find(&rs).Error; err != nil {
		return nil, err
	}
	for _, r := range rs {
		p.roster[planKey(r.EmployeeID, r.WorkDate)] = r.WorkScheduleID
	}
	var os []models.ScheduleOverride
	if err := db.WithContext(ctx).Where("employee_id IN ? AND start_date <= ? AND end_date >= ?", empIDs, to, from).
		Order("start_date").Find(&os).Error; err != nil {
		return nil, err
	}
	for _, o := range os {
		p.overrides[o.EmployeeID] = append(p.overrides[o.EmployeeID], o)
	}
	return p, nil
}

// Usual is the employee's regular shift (the company default if none is set).
func (p *Planner) Usual(emp *models.Employee) *models.WorkSchedule {
	if emp.WorkScheduleID != nil {
		if ws := p.shifts[*emp.WorkScheduleID]; ws != nil {
			return ws
		}
	}
	return p.fallback
}

// Shift looks up a shift by id (nil if unknown).
func (p *Planner) Shift(id uint64) *models.WorkSchedule { return p.shifts[id] }

// Day: special schedule > roster > usual shift on its work days.
func (p *Planner) Day(emp *models.Employee, d time.Time) Day {
	for i := range p.overrides[emp.ID] {
		if o := &p.overrides[emp.ID][i]; !d.Before(o.StartDate) && !d.After(o.EndDate) {
			return p.special(o, d)
		}
	}
	if id, ok := p.roster[planKey(emp.ID, d)]; ok {
		if id == nil {
			return Day{Source: PlanRoster}
		}
		if ws := p.shifts[*id]; ws != nil {
			day := ShiftDay(ws)
			day.Source = PlanRoster
			return day
		}
	}
	ws := p.Usual(emp)
	day := ShiftDay(ws)
	day.Work = ParseWorkDays(ws.WorkDays)[timeutil.ISOWeekday(d)]
	day.Source = PlanDefault
	return day
}

func (p *Planner) special(o *models.ScheduleOverride, d time.Time) Day {
	id := o.ID
	off := Day{Source: PlanSpecial, OverrideID: &id, Note: o.Reason}
	if o.Kind == models.OverrideOff || !ParseWorkDays(o.WorkDays)[timeutil.ISOWeekday(d)] {
		return off
	}
	var day Day
	switch {
	case o.Kind == models.OverrideShift && o.WorkScheduleID != nil && p.shifts[*o.WorkScheduleID] != nil:
		day = ShiftDay(p.shifts[*o.WorkScheduleID])
	case o.Kind == models.OverrideCustom && o.StartTime != nil && o.EndTime != nil:
		day = Day{Work: true, Start: *o.StartTime, End: *o.EndTime, ShiftName: "Custom hours", Color: "#7c3aed",
			CrossDay: o.WorkType != nil && *o.WorkType == models.WorkCrossDay}
		if o.GraceMinutes != nil {
			day.Grace = *o.GraceMinutes
		}
	default:
		return off
	}
	day.Source, day.OverrideID, day.Note = PlanSpecial, &id, o.Reason
	return day
}

// WorkingDays counts scheduled working days in [from, to], excluding holidays.
func (p *Planner) WorkingDays(emp *models.Employee, from, to time.Time, isHoliday func(time.Time) bool) int {
	n := 0
	for d := from; !d.After(to); d = d.AddDate(0, 0, 1) {
		if p.Day(emp, d).Work && !isHoliday(d) {
			n++
		}
	}
	return n
}

// Halves splits a working day's shift at its midpoint: the first and second half
// as HH:MM (company time). For a cross-day shift (22:00-06:00) the halves are
// 22:00-02:00 and 02:00-06:00; secondNext is true when the second half starts
// after midnight.
func (d Day) Halves(date time.Time, loc *time.Location) (first, second [2]string, secondNext bool, err error) {
	start, err := d.StartAt(date, loc)
	if err != nil {
		return first, second, false, err
	}
	end, err := d.EndAt(date, loc)
	if err != nil {
		return first, second, false, err
	}
	mid := start.Add(end.Sub(start) / 2).Truncate(time.Minute)
	m := mid.In(loc).Format("15:04")
	return [2]string{d.Start, m}, [2]string{m, d.End}, timeutil.DateOf(mid, loc).After(date), nil
}

// WithHalfOff is the part of the day still to be worked when half of it is on
// leave: late and early-leave minutes are measured against these hours.
func (d Day) WithHalfOff(half string, date time.Time, loc *time.Location) Day {
	if !d.Work {
		return d
	}
	first, second, secondNext, err := d.Halves(date, loc)
	if err != nil {
		return d
	}
	switch half {
	case models.LeaveFirstHalf: // works the second half
		d.Start, d.StartNext = second[0], secondNext
	case models.LeaveSecondHalf: // works the first half
		d.End = first[1]
		d.CrossDay = secondNext
	}
	return d
}
