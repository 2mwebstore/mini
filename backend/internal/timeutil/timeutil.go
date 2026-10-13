// Package timeutil centralises timezone and calendar-date handling.
//
// Convention: a "date" is a time.Time at 00:00:00 UTC whose Y-M-D is the calendar
// day in the company timezone. That is exactly what MySQL DATE columns round-trip
// to with loc=UTC, so comparisons never drift across timezones.
package timeutil

import (
	"fmt"
	"strconv"
	"strings"
	"time"

	_ "time/tzdata" // embed tz database so containers don't need tzdata installed
)

func LoadLocation(name string) *time.Location {
	if loc, err := time.LoadLocation(name); err == nil {
		return loc
	}
	return time.FixedZone("ICT", 7*3600)
}

// DateOf returns the calendar date of instant t in loc.
func DateOf(t time.Time, loc *time.Location) time.Time {
	l := t.In(loc)
	return time.Date(l.Year(), l.Month(), l.Day(), 0, 0, 0, 0, time.UTC)
}

// At combines a calendar date with an HH:MM wall-clock time in loc and returns the UTC instant.
func At(date time.Time, hhmm string, loc *time.Location) (time.Time, error) {
	h, m, err := ParseHHMM(hhmm)
	if err != nil {
		return time.Time{}, err
	}
	return time.Date(date.Year(), date.Month(), date.Day(), h, m, 0, 0, loc).UTC(), nil
}

func ParseHHMM(s string) (int, int, error) {
	parts := strings.Split(strings.TrimSpace(s), ":")
	if len(parts) != 2 {
		return 0, 0, fmt.Errorf("invalid time %q, expected HH:MM", s)
	}
	h, err1 := strconv.Atoi(parts[0])
	m, err2 := strconv.Atoi(parts[1])
	if err1 != nil || err2 != nil || h < 0 || h > 23 || m < 0 || m > 59 {
		return 0, 0, fmt.Errorf("invalid time %q, expected HH:MM", s)
	}
	return h, m, nil
}

// ParseDate accepts DD-MM-YYYY, DD/MM/YYYY and YYYY-MM-DD.
func ParseDate(s string) (time.Time, error) {
	s = strings.TrimSpace(s)
	for _, layout := range []string{"02-01-2006", "02/01/2006", "2006-01-02", "2-1-2006", "2/1/2006"} {
		if t, err := time.Parse(layout, s); err == nil {
			return time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, time.UTC), nil
		}
	}
	return time.Time{}, fmt.Errorf("invalid date %q, use DD-MM-YYYY", s)
}

func FormatDate(d time.Time) string { return d.Format("02 Jan 2006") }

func FormatClock(t *time.Time, loc *time.Location) string {
	if t == nil {
		return "—"
	}
	return t.In(loc).Format("03:04 PM")
}

func FormatClock24(t *time.Time, loc *time.Location) string {
	if t == nil {
		return "—"
	}
	return t.In(loc).Format("15:04")
}

// FormatMinutes renders 515 as "8h 35m".
func FormatMinutes(m int) string {
	if m < 0 {
		m = 0
	}
	return fmt.Sprintf("%dh %02dm", m/60, m%60)
}

// FormatMinutesThousands renders large totals as "2,845h 30m".
func FormatMinutesThousands(m int) string {
	if m < 0 {
		m = 0
	}
	h := strconv.Itoa(m / 60)
	var b strings.Builder
	for i, c := range h {
		if i > 0 && (len(h)-i)%3 == 0 {
			b.WriteByte(',')
		}
		b.WriteRune(c)
	}
	return fmt.Sprintf("%sh %02dm", b.String(), m%60)
}

// MonthRange returns the first and last calendar date of d's month.
func MonthRange(d time.Time) (time.Time, time.Time) {
	first := time.Date(d.Year(), d.Month(), 1, 0, 0, 0, 0, time.UTC)
	return first, first.AddDate(0, 1, -1)
}

// WeekRange returns Monday..Sunday containing d.
func WeekRange(d time.Time) (time.Time, time.Time) {
	wd := int(d.Weekday())
	if wd == 0 {
		wd = 7
	}
	start := d.AddDate(0, 0, -(wd - 1))
	return start, start.AddDate(0, 0, 6)
}

// ISOWeekday returns 1=Monday .. 7=Sunday.
func ISOWeekday(d time.Time) int {
	wd := int(d.Weekday())
	if wd == 0 {
		return 7
	}
	return wd
}
