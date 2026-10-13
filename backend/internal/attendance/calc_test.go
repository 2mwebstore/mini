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

func TestLateMinutes(t *testing.T) {
	s := DefaultSchedule() // 09:00, grace 10
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
	strict := Schedule{Start: "09:00", End: "17:30", GraceMinutes: 0, WorkDays: s.WorkDays}
	if got := strict.LateMinutes(d, at(d, "09:01"), pp); got != 1 {
		t.Errorf("no grace: 09:01 late=%d want 1", got)
	}
}

func TestEarlyLeaveAndWorking(t *testing.T) {
	s := DefaultSchedule() // ends 17:30
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

func TestTimezoneDateBoundary(t *testing.T) {
	// 2026-10-08 23:30 UTC is already 2026-10-09 06:30 in Phnom Penh.
	utc := time.Date(2026, 10, 8, 23, 30, 0, 0, time.UTC)
	if got := timeutil.DateOf(utc, pp).Format("2006-01-02"); got != "2026-10-09" {
		t.Fatalf("DateOf=%s", got)
	}
}

func TestWorkDays(t *testing.T) {
	s := DefaultSchedule()
	sat := time.Date(2026, 10, 10, 0, 0, 0, 0, time.UTC)
	mon := time.Date(2026, 10, 12, 0, 0, 0, 0, time.UTC)
	if s.IsWorkDay(sat) || !s.IsWorkDay(mon) {
		t.Fatal("Mon-Fri schedule misclassified")
	}
	six := ScheduleFrom(&models.WorkSchedule{StartTime: "08:00", EndTime: "17:00", WorkDays: "1,2,3,4,5,6"})
	if !six.IsWorkDay(sat) {
		t.Fatal("Mon-Sat schedule should include Saturday")
	}
	hol := func(d time.Time) bool { return d.Day() == 13 }
	if n := WorkingDays(&models.Employee{}, mon, mon.AddDate(0, 0, 6), hol); n != 4 {
		t.Fatalf("working days=%d want 4 (Mon-Fri minus one holiday)", n)
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
