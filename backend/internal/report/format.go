package report

import (
	"fmt"
	"strings"

	"attendance/backend/internal/models"
	"attendance/backend/internal/telegram"
	"attendance/backend/internal/timeutil"
)

const divider = "────────────────────"

func confIcon(r DayRow) string {
	switch {
	case r.Status == models.StatusLeave:
		return "🏖️"
	case r.Status == models.StatusAbsent || r.Status == DayNotYet:
		return "🔴"
	case r.Confirmation == models.ConfPending:
		return "🟡"
	case r.Confirmation == models.ConfRejected:
		return "❌"
	default:
		return "✅"
	}
}

type DayCounts struct {
	Staff, Confirmed, Pending, Rejected, Leave, Absent, NotYet, Late, MissingOut int
}

func CountDay(rows []DayRow) DayCounts {
	var c DayCounts
	for _, r := range rows {
		c.Staff++
		switch {
		case r.Status == models.StatusLeave:
			c.Leave++
		case r.Status == models.StatusAbsent:
			c.Absent++
		case r.Status == DayNotYet:
			c.NotYet++
		case r.Confirmation == models.ConfPending:
			c.Pending++
		case r.Confirmation == models.ConfRejected:
			c.Rejected++
		default:
			c.Confirmed++
		}
		if r.LateMinutes > 0 {
			c.Late++
		}
		if r.CheckInAt != nil && r.CheckOutAt == nil {
			c.MissingOut++
		}
	}
	return c
}

// DayText renders the "/report today" message (also used by the automated daily report).
func DayText(rep *Report, title string, withAttention bool) string {
	var b strings.Builder
	fmt.Fprintf(&b, "📊 <b>%s</b>\n%s\n", title, timeutil.FormatDate(rep.From))
	if len(rep.Rows) == 0 {
		b.WriteString("\nNo working staff scheduled for this day.")
		return b.String()
	}
	c := CountDay(rep.Rows)
	fmt.Fprintf(&b, "👥 Total Staff: %d\n✅ Confirmed: %d\n🟡 Pending: %d\n", c.Staff, c.Confirmed, c.Pending)
	if c.Rejected > 0 {
		fmt.Fprintf(&b, "❌ Rejected: %d\n", c.Rejected)
	}
	fmt.Fprintf(&b, "🏖️ Leave: %d\n", c.Leave)
	if c.NotYet > 0 {
		fmt.Fprintf(&b, "🔴 Not checked in: %d\n", c.NotYet)
	}
	if c.Absent > 0 {
		fmt.Fprintf(&b, "🔴 Absent: %d\n", c.Absent)
	}
	fmt.Fprintf(&b, "⏰ Late: %d\n%s\n👤 <b>STAFF</b>\n", c.Late, divider)
	for i, r := range rep.Rows {
		name := telegram.Esc(short(r.FullName, 16))
		switch r.Status {
		case models.StatusLeave:
			fmt.Fprintf(&b, "%d. %s  🏖️ %s\n", i+1, name, telegram.Esc(or(r.LeaveType, "Leave")))
		case models.StatusAbsent, DayNotYet:
			fmt.Fprintf(&b, "%d. %s  — → — 🔴\n", i+1, name)
		default:
			late := ""
			if r.LateMinutes > 0 {
				late = fmt.Sprintf(" ⏰%dm", r.LateMinutes)
			}
			fmt.Fprintf(&b, "%d. %s  %s → %s %s%s\n", i+1, name, timeutil.FormatClock24(r.CheckInAt, rep.Loc),
				timeutil.FormatClock24(r.CheckOutAt, rep.Loc), confIcon(r), late)
		}
	}
	if withAttention {
		var att []string
		for _, r := range rep.Rows {
			n := telegram.Esc(r.FullName)
			switch {
			case r.Status == models.StatusAbsent || r.Status == DayNotYet:
				att = append(att, n+" - No check-in")
			case r.Status == models.StatusLeave:
			default:
				if r.Confirmation == models.ConfPending {
					att = append(att, n+" - Pending confirmation")
				}
				if r.Confirmation == models.ConfRejected {
					att = append(att, n+" - Attendance rejected")
				}
				if r.LateMinutes > 0 {
					att = append(att, fmt.Sprintf("%s - Late %d minutes", n, r.LateMinutes))
				}
				if r.CheckOutAt == nil {
					att = append(att, n+" - No check-out")
				}
			}
		}
		if len(att) > 0 {
			fmt.Fprintf(&b, "%s\n⚠️ <b>NEED ATTENTION</b>\n%s\n", divider, strings.Join(att, "\n"))
		}
	}
	return strings.TrimRight(b.String(), "\n")
}

func short(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n-1]) + "…"
}

func or(v, def string) string {
	if v == "" {
		return def
	}
	return v
}
