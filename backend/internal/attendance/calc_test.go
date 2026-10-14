package attendance

import (
	"testing"
	"time"

	"attendance/backend/internal/models"
	"attendance/backend/internal/timeutil"
)

var pp = timeutil.LoadLocation("Asia/Phnom_Penh")

func at(date time.Time, hhmm string) time.Time {
	t, _ := timeutil.At(date, hhmm, pp)
	return t
}

func office() Day {
	ws := DefaultShift() // 09:00-17:30, grace 10
	return ShiftDay(&ws)
}

func TestLateMinutes(t *testing.T) {
	s := office()
	d := time.Date(2026, 10, 8, 0, 0, 0, 0, time.UTC)
	cases := []struct {
		in   string
		want int
	}{
		{"08:55", 0}, {"09:00", 0}, {"09:01", 0}, {"09:10", 0}, // within grace
		{"09:11", 11}, {"09:15", 15}, {"09:25", 25}, {"13:00", 240},
	}
	for _, c := range cases {
		if got := s.LateMinutes(d, at(d, c.in), pp); got != c.want {
			t.Errorf("check-in %s: late=%d want %d", c.in, got, c.want)
		}
	}
	strict := s
	strict.Grace = 0
	if got := strict.LateMinutes(d, at(d, "09:01"), pp); got != 1 {
		t.Errorf("no grace: 09:01 late=%d want 1", got)
	}
}

func TestEarlyLeaveAndWorking(t *testing.T) {
	s := office() // ends 17:30
	d := time.Date(2026, 10, 8, 0, 0, 0, 0, time.UTC)
	if got := s.EarlyLeaveMinutes(d, at(d, "17:32"), pp); got != 0 {
		t.Errorf("after end: early=%d", got)
	}
	if got := s.EarlyLeaveMinutes(d, at(d, "17:00"), pp); got != 30 {
		t.Errorf("17:00: early=%d want 30", got)
	}
	// spec example: 08:57 -> 17:32 = 8h 35m
	if got := WorkingMinutes(at(d, "08:57"), at(d, "17:32")); got != 515 {
		t.Errorf("working=%d want 515", got)
	}
	if timeutil.FormatMinutes(515) != "8h 35m" {
		t.Errorf("format=%s", timeutil.FormatMinutes(515))
	}
	if WorkingMinutes(at(d, "10:00"), at(d, "09:00")) != 0 {
		t.Error("negative working time must be 0")
	}
}

func TestNightShift(t *testing.T) {
	night := Day{Work: true, Start: "22:00", End: "06:00", Grace: 15, CrossDay: true}
	d := time.Date(2026, 10, 8, 0, 0, 0, 0, time.UTC)
	next := d.AddDate(0, 0, 1)
	end, _ := night.EndAt(d, pp)
	if want := at(next, "06:00"); !end.Equal(want) {
		t.Fatalf("end=%s want %s (next morning)", end, want)
	}
	cases := []struct {
		name string
		got  int
		want int
	}{
		{"in 22:10 (grace)", night.LateMinutes(d, at(d, "22:10"), pp), 0},
		{"in 22:40", night.LateMinutes(d, at(d, "22:40"), pp), 40},
		{"in 00:30 next day", night.LateMinutes(d, at(next, "00:30"), pp), 150},
		{"out 05:30 next day", night.EarlyLeaveMinutes(d, at(next, "05:30"), pp), 30},
		{"out 06:10 next day", night.EarlyLeaveMinutes(d, at(next, "06:10"), pp), 0},
		{"out 23:30 same day", night.EarlyLeaveMinutes(d, at(d, "23:30"), pp), 390},
		{"worked 21:55 -> 06:05", WorkingMinutes(at(d, "21:55"), at(next, "06:05")), 490},
	}
	for _, c := range cases {
		if c.got != c.want {
			t.Errorf("%s: %d, want %d", c.name, c.got, c.want)
		}
	}
}

func TestTimezoneDateBoundary(t *testing.T) {
	// 2026-10-08 23:30 UTC is already 2026-10-09 06:30 in Phnom Penh.
	utc := time.Date(2026, 10, 8, 23, 30, 0, 0, time.UTC)
	if got := timeutil.DateOf(utc, pp).Format("2006-01-02"); got != "2026-10-09" {
		t.Fatalf("DateOf=%s", got)
	}
}

// Monday 12 Oct 2026 .. Sunday 18 Oct 2026
func week(i int) time.Time { return time.Date(2026, 10, 12+i, 0, 0, 0, 0, time.UTC) }

func TestPlannerPrecedence(t *testing.T) {
	officeWS := DefaultShift()
	officeWS.ID, officeWS.IsDefault = 1, true
	nightWS := models.WorkSchedule{ID: 2, Name: "Night", StartTime: "22:00", EndTime: "06:00", WorkType: models.WorkCrossDay,
		GraceMinutes: 15, WorkDays: "1,2,3,4,5,6"}
	two := uint64(2)
	start, end, wt, grace := "13:00", "21:00", models.WorkNormal, 5
	p := &Planner{
		shifts:   map[uint64]*models.WorkSchedule{1: &officeWS, 2: &nightWS},
		fallback: &officeWS,
		roster: map[string]*uint64{
			planKey(7, week(1)): &two, // Tue: night shift
			planKey(7, week(2)): nil,  // Wed: day off
			planKey(7, week(3)): &two, // Thu: night shift, but a special schedule wins
		},
		overrides: map[uint64][]models.ScheduleOverride{7: {{ID: 9, EmployeeID: 7, StartDate: week(3), EndDate: week(5),
			Kind: models.OverrideCustom, StartTime: &start, EndTime: &end, WorkType: &wt, GraceMinutes: &grace, WorkDays: "4,6", Reason: "Stock count"}}},
	}
	emp := &models.Employee{ID: 7} // no usual shift: the company default
	check := func(i int, work bool, source, startAt string, cross bool) {
		t.Helper()
		d := p.Day(emp, week(i))
		if d.Work != work || d.Source != source || (work && d.Start != startAt) || d.CrossDay != cross {
			t.Errorf("%s: %+v", week(i).Format("Mon 02"), d)
		}
	}
	check(0, true, PlanDefault, "09:00", false) // Mon: usual office hours
	check(1, true, PlanRoster, "22:00", true)   // Tue: roster night shift
	check(2, false, PlanRoster, "", false)      // Wed: roster day off
	check(3, true, PlanSpecial, "13:00", false) // Thu: special custom hours beat the roster
	check(4, false, PlanSpecial, "", false)     // Fri: special schedule, not one of its days
	check(5, true, PlanSpecial, "13:00", false) // Sat: special custom hours
	check(6, false, PlanDefault, "", false)     // Sun: office hours are Mon-Fri
	if d := p.Day(emp, week(3)); d.Note != "Stock count" || d.Grace != 5 || d.OverrideID == nil || *d.OverrideID != 9 {
		t.Errorf("special details: %+v", d)
	}

	nightWorker := &models.Employee{ID: 8, WorkScheduleID: &two}
	if d := p.Day(nightWorker, week(5)); !d.Work || !d.CrossDay || d.ShiftName != "Night" || d.Source != PlanDefault {
		t.Errorf("night worker Saturday: %+v", d)
	}
	if d := p.Day(nightWorker, week(6)); d.Work {
		t.Errorf("night worker Sunday should be off: %+v", d)
	}

	hol := func(d time.Time) bool { return d.Equal(week(0)) }
	// Mon holiday, Tue night, Wed off, Thu special, Fri off, Sat special, Sun off = 3
	if n := p.WorkingDays(emp, week(0), week(6), hol); n != 3 {
		t.Errorf("working days=%d want 3", n)
	}
}

func TestParseWorkDays(t *testing.T) {
	set := ParseWorkDays("1, 2,6,9,x")
	if len(set) != 3 || !set[1] || !set[2] || !set[6] {
		t.Fatalf("%v", set)
	}
	sat := time.Date(2026, 10, 10, 0, 0, 0, 0, time.UTC)
	ws := models.WorkSchedule{StartTime: "08:00", EndTime: "17:00", WorkType: models.WorkNormal, WorkDays: "1,2,3,4,5,6"}
	p := &Planner{shifts: map[uint64]*models.WorkSchedule{}, fallback: &ws}
	if !p.Day(&models.Employee{}, sat).Work {
		t.Fatal("Mon-Sat shift should include Saturday")
	}
}

func TestDeriveStatusAndConfirmation(t *testing.T) {
	if DeriveStatus(0, 0) != models.StatusPresent || DeriveStatus(5, 0) != models.StatusLate ||
		DeriveStatus(0, 5) != models.StatusEarlyLeave || DeriveStatus(5, 5) != models.StatusLate {
		t.Fatal("DeriveStatus wrong")
	}
	c := func(s ...string) []models.AttendanceConfirmation {
		var out []models.AttendanceConfirmation
		for _, x := range s {
			out = append(out, models.AttendanceConfirmation{Status: x})
		}
		return out
	}
	if DeriveConfirmation(c(models.ConfConfirmed, models.ConfPending)) != models.ConfPending ||
		DeriveConfirmation(c(models.ConfConfirmed, models.ConfRejected)) != models.ConfRejected ||
		DeriveConfirmation(c(models.ConfConfirmed, models.ConfConfirmed)) != models.ConfConfirmed {
		t.Fatal("DeriveConfirmation wrong")
	}
}

func TestDistance(t *testing.T) {
	// ~111 m per 0.001 degree latitude
	d := DistanceMeters(11.5564, 104.9282, 11.5574, 104.9282)
	if d < 105 || d > 117 {
		t.Fatalf("distance=%.1f", d)
	}
	if DistanceMeters(11.5564, 104.9282, 11.5564, 104.9282) != 0 {
		t.Fatal("same point must be 0")
	}
}

func TestBlockers(t *testing.T) {
	now := time.Now()
	rejectedIn := models.Attendance{CheckInAt: &now, Confirmations: []models.AttendanceConfirmation{{ID: 1, Type: models.TypeCheckIn, Status: models.ConfRejected}}}
	if checkInBlocker(true, &rejectedIn) != nil {
		t.Error("re-check-in after rejection should be allowed")
	}
	if checkOutBlocker(true, &rejectedIn) != errInRejected {
		t.Error("check-out after rejected check-in must be blocked")
	}
	okIn := models.Attendance{CheckInAt: &now, Confirmations: []models.AttendanceConfirmation{{ID: 1, Type: models.TypeCheckIn, Status: models.ConfPending}}}
	if checkInBlocker(true, &okIn) != errAlreadyIn {
		t.Error("double check-in must be blocked")
	}
	if checkOutBlocker(false, nil) != errNoCheckIn {
		t.Error("check-out without check-in must be blocked")
	}
	if checkOutBlocker(true, &okIn) != nil {
		t.Error("check-out after valid check-in should be allowed")
	}
	done := okIn
	done.CheckOutAt = &now
	done.Confirmations = append(done.Confirmations, models.AttendanceConfirmation{ID: 2, Type: models.TypeCheckOut, Status: models.ConfConfirmed})
	if checkOutBlocker(true, &done) != errAlreadyOut {
		t.Error("double check-out must be blocked")
	}
	leaveDay := models.Attendance{Status: models.StatusLeave}
	if checkInBlocker(true, &leaveDay) != errOnLeave {
		t.Error("check-in on leave day must be blocked")
	}
}
