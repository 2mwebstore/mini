package attendance

import (
	"math"
	"strconv"
	"strings"
	"time"

	"attendance/backend/internal/models"
	"attendance/backend/internal/timeutil"
)

// Schedule is the parsed, validated form of a work_schedules row.
type Schedule struct {
	Start        string
	End          string
	GraceMinutes int
	WorkDays     map[int]bool // ISO weekday
}

func ScheduleFrom(ws *models.WorkSchedule) Schedule {
	if ws == nil {
		return DefaultSchedule()
	}
	s := Schedule{Start: ws.StartTime, End: ws.EndTime, GraceMinutes: ws.GraceMinutes, WorkDays: map[int]bool{}}
	for _, p := range strings.Split(ws.WorkDays, ",") {
		if d, err := strconv.Atoi(strings.TrimSpace(p)); err == nil && d >= 1 && d <= 7 {
			s.WorkDays[d] = true
		}
	}
	if len(s.WorkDays) == 0 {
		s.WorkDays = DefaultSchedule().WorkDays
	}
	return s
}

func DefaultSchedule() Schedule {
	return Schedule{Start: "09:00", End: "17:30", GraceMinutes: 10, WorkDays: map[int]bool{1: true, 2: true, 3: true, 4: true, 5: true}}
}

func (s Schedule) IsWorkDay(date time.Time) bool { return s.WorkDays[timeutil.ISOWeekday(date)] }

// LateMinutes: an employee is late only after start+grace, but once late the
// minutes are counted from the scheduled start (09:15 with 10 min grace = 15 min late).
func (s Schedule) LateMinutes(date time.Time, checkIn time.Time, loc *time.Location) int {
	start, err := timeutil.At(date, s.Start, loc)
	if err != nil {
		return 0
	}
	if !checkIn.After(start.Add(time.Duration(s.GraceMinutes) * time.Minute)) {
		return 0
	}
	return int(checkIn.Sub(start) / time.Minute)
}

// EarlyLeaveMinutes: minutes between check-out and scheduled end (0 if on/after end).
func (s Schedule) EarlyLeaveMinutes(date time.Time, checkOut time.Time, loc *time.Location) int {
	end, err := timeutil.At(date, s.End, loc)
	if err != nil || !checkOut.Before(end) {
		return 0
	}
	return int(math.Ceil(end.Sub(checkOut).Minutes()))
}

// WorkingMinutes is the plain elapsed time between check-in and check-out.
func WorkingMinutes(in, out time.Time) int {
	if !out.After(in) {
		return 0
	}
	return int(out.Sub(in) / time.Minute)
}

// DeriveStatus picks the day's headline status. Late and early-leave minutes are
// always stored separately, so a day that is both still reports both in totals.
func DeriveStatus(lateMinutes, earlyMinutes int) string {
	switch {
	case lateMinutes > 0:
		return models.StatusLate
	case earlyMinutes > 0:
		return models.StatusEarlyLeave
	default:
		return models.StatusPresent
	}
}

// DeriveConfirmation folds the per-event reviews into the attendance-level status.
func DeriveConfirmation(confs []models.AttendanceConfirmation) string {
	if len(confs) == 0 {
		return models.ConfConfirmed
	}
	pending := false
	for _, c := range confs {
		switch c.Status {
		case models.ConfRejected:
			return models.ConfRejected
		case models.ConfPending:
			pending = true
		}
	}
	if pending {
		return models.ConfPending
	}
	return models.ConfConfirmed
}

// DistanceMeters is the great-circle distance between two coordinates.
func DistanceMeters(lat1, lng1, lat2, lng2 float64) float64 {
	const r = 6371000.0
	rad := math.Pi / 180
	dLat := (lat2 - lat1) * rad
	dLng := (lng2 - lng1) * rad
	a := math.Sin(dLat/2)*math.Sin(dLat/2) + math.Cos(lat1*rad)*math.Cos(lat2*rad)*math.Sin(dLng/2)*math.Sin(dLng/2)
	return 2 * r * math.Asin(math.Min(1, math.Sqrt(a)))
}
