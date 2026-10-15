package report

import (
	"fmt"
	"math"
	"strings"

	"attendance/backend/internal/models"
	"attendance/backend/internal/timeutil"

	"github.com/xuri/excelize/v2"
)

// The Payroll sheet is laid out like the CRM's "Detail Report Payroll": a blue title
// bar, one column per day with the check-in – check-out times (absent days pink), then
// each person's payroll totals — days, minutes late / early / after the shift,
// absences, leave, days worked off and approved overtime hours.

const (
	sheetPayroll = "Payroll"

	payTitleFill   = "#6280B4"
	payHeadFill    = "#F3F4F6"
	payLine        = "#000000"
	payRed         = "#CA3A31"
	payAmber       = "#E2B13C"
	payAbsentFill  = "#F4CCCC"
	payLeaveFill   = "#FCE8B2"
	payOffFill     = "#E5E7EB"
	payHolidayFill = "#DBEAFE"
	payWorkedFill  = "#D9EAD3" // worked on a day off or a holiday
	payFirstRow    = 4         // rows 1–3: title and the two header rows
)

// The totals after the day columns: a group heading over its sub-headings.
var payGroups = []struct {
	title string
	subs  []string
}{
	{"Day Work", []string{"Count"}},
	{"Attendance", []string{"Count"}},
	{"Work Time", []string{"Q-mn"}},
	{"Checkin Late", []string{"Q-mn", "Count"}},
	{"Checkout Early", []string{"Q-mn", "Count"}},
	{"Checkout Overtime", []string{"Q-mn", "Count"}},
	{"Absent", []string{"Absent"}},
	{"Other", []string{"Leave", "AL", "OT Day", "OT Day x2"}},
	{"Overtime", []string{"OT Hours", "OT Hours x2"}},
}

// Sub-headings in colour, as in the CRM.
var paySubColor = map[string]string{"Leave": payAmber, "AL": payRed, "OT Day": payAmber, "OT Day x2": payAmber}

// payTotals: what the payroll columns add up per person, beyond the Summary.
type payTotals struct {
	early, after, afterDays int
	leave, annual           float64 // days; a half day counts 0.5
	offDays, holidays       int     // days worked on a day off / a public holiday
	ot, otDouble            int     // approved overtime minutes: on working days / on days off and holidays
}

func isAnnual(code string) bool {
	return strings.EqualFold(code, "ANNUAL") || strings.EqualFold(code, "AL")
}

// leaveShort: "AL" for annual leave, else the leave type's name without "(first half)".
func leaveShort(r *DayRow) string {
	if isAnnual(r.LeaveCode) {
		return "AL"
	}
	name := r.LeaveType
	if i := strings.Index(name, " ("); r.LeaveHalf != "" && i > 0 {
		name = name[:i]
	}
	if name == "" {
		name = "Leave"
	}
	return name
}

// Day-cell kinds (they pick the fill).
const (
	dayPlain = iota
	dayAbsent
	dayLeave
	dayOff
	dayHoliday
	dayWorkedOff
)

// payDay: what one person's day shows in the grid. r is nil when there is no row
// (a day off, a holiday, a day before they joined or still to come).
func payDay(rep *Report, r *DayRow, off string, isOff bool) (text string, kind int, late bool) {
	if r != nil && r.CheckInAt != nil {
		text = timeutil.FormatClock24(r.CheckInAt, rep.Loc)
		if r.CheckOutAt != nil {
			text += " - " + timeutil.FormatClock24(r.CheckOutAt, rep.Loc)
		}
		switch {
		case isOff:
			kind = dayWorkedOff
		case r.LeaveHalf != "":
			kind = dayLeave
		}
		return text, kind, r.LateMinutes > 0
	}
	if r != nil {
		switch r.Status {
		case models.StatusLeave:
			return leaveShort(r), dayLeave, false
		case models.StatusAbsent:
			if r.LeaveHalf != "" {
				return "½ " + leaveShort(r), dayAbsent, false
			}
			return "", dayAbsent, false
		case models.StatusHoliday:
			return "Holiday", dayHoliday, false
		case models.StatusWeekend:
			return "OFF", dayOff, false
		}
		return "", dayPlain, false // not checked in yet today
	}
	switch {
	case isOff && off != "":
		return "Holiday", dayHoliday, false
	case isOff:
		return "OFF", dayOff, false
	}
	return "", dayPlain, false
}

func (s *styles) pay(key string, st *excelize.Style) int {
	if st.Border == nil {
		st.Border = border(payLine)
	}
	if st.Font == nil {
		st.Font = &excelize.Font{Color: "#000000"}
	}
	if st.Alignment == nil {
		st.Alignment = &excelize.Alignment{Horizontal: "center", Vertical: "center"}
	}
	return s.get("pay|"+key, st)
}

func fill(color string) excelize.Fill {
	return excelize.Fill{Type: "pattern", Color: []string{color}, Pattern: 1}
}

func writePayroll(f *excelize.File, st *styles, rep *Report) error {
	const sh = sheetPayroll
	var days []string // yyyy-mm-dd
	for d := rep.From; !d.After(rep.To); d = d.AddDate(0, 0, 1) {
		days = append(days, d.Format("2006-01-02"))
	}
	firstSum := 3 + len(days) // the first totals column
	subs := 0
	for _, g := range payGroups {
		subs += len(g.subs)
	}
	lastCol := firstSum + subs - 1
	cell := func(col, row int) string { return fmt.Sprintf("%s%d", colName(col), row) }

	// ---- title bar ----
	// "October 2026" for a month (or the month so far), else the dates.
	title := "Detail Report Payroll " + rep.From.Format("January 2006")
	switch {
	case rep.From.Format("200601") != rep.To.Format("200601"):
		title = fmt.Sprintf("Detail Report Payroll %s – %s", rep.From.Format("02 Jan 2006"), rep.To.Format("02 Jan 2006"))
	case rep.From.Equal(rep.To):
		title = "Detail Report Payroll " + rep.From.Format("02 January 2006")
	case rep.From.Day() != 1:
		title = fmt.Sprintf("Detail Report Payroll %s – %s", rep.From.Format("02"), rep.To.Format("02 January 2006"))
	}
	_ = f.SetCellValue(sh, "A1", title)
	_ = f.MergeCell(sh, "A1", cell(lastCol, 1))
	_ = f.SetCellStyle(sh, "A1", cell(lastCol, 1), st.pay("title", &excelize.Style{
		Font: &excelize.Font{Bold: true, Size: 16, Color: "#FFFFFF"}, Fill: fill(payTitleFill),
		Border: []excelize.Border{{Type: "bottom", Color: payLine, Style: 1}}}))
	_ = f.SetRowHeight(sh, 1, 34)

	// ---- the two header rows ----
	head := st.pay("head", &excelize.Style{Font: &excelize.Font{Bold: true, Color: "#000000"}, Fill: fill(payHeadFill),
		Alignment: &excelize.Alignment{Horizontal: "center", Vertical: "center", WrapText: true}})
	for i, h := range []string{"ID", "Name"} {
		_ = f.SetCellValue(sh, cell(i+1, 2), h)
		_ = f.MergeCell(sh, cell(i+1, 2), cell(i+1, 3))
	}
	// Day headings are dates shown as "01" (or "01/10" across months): real dates, so
	// Excel doesn't flag them as numbers stored as text.
	dayFmt := "dd"
	if rep.From.Format("200601") != rep.To.Format("200601") {
		dayFmt = "dd/mm"
	}
	dayHead := st.pay("dayHead"+dayFmt, &excelize.Style{Font: &excelize.Font{Bold: true, Color: "#000000"}, Fill: fill(payHeadFill), CustomNumFmt: &dayFmt})
	for i := range days {
		d := rep.From.AddDate(0, 0, i)
		_ = f.SetCellValue(sh, cell(3+i, 2), d)
		_ = f.SetCellStyle(sh, cell(3+i, 2), cell(3+i, 2), dayHead)
	}
	_ = f.SetCellValue(sh, cell(3, 3), "Check in - Check out")
	_ = f.MergeCell(sh, cell(3, 3), cell(firstSum-1, 3))
	col := firstSum
	for _, g := range payGroups {
		_ = f.SetCellValue(sh, cell(col, 2), g.title)
		if len(g.subs) > 1 {
			_ = f.MergeCell(sh, cell(col, 2), cell(col+len(g.subs)-1, 2))
		}
		for _, sub := range g.subs {
			_ = f.SetCellValue(sh, cell(col, 3), sub)
			if c, ok := paySubColor[sub]; ok {
				_ = f.SetCellStyle(sh, cell(col, 3), cell(col, 3), st.pay("sub"+c, &excelize.Style{
					Font: &excelize.Font{Bold: true, Color: c}, Fill: fill(payHeadFill)}))
			}
			col++
		}
	}
	// header style everywhere but the cells styled above
	_ = f.SetCellStyle(sh, "A2", "B3", head)
	_ = f.SetCellStyle(sh, cell(3, 3), cell(firstSum-1, 3), head)
	_ = f.SetCellStyle(sh, cell(firstSum, 2), cell(lastCol, 2), head)
	col = firstSum
	for _, g := range payGroups {
		for _, sub := range g.subs {
			if _, ok := paySubColor[sub]; !ok {
				_ = f.SetCellStyle(sh, cell(col, 3), cell(col, 3), head)
			}
			col++
		}
	}
	_ = f.SetRowHeight(sh, 2, 30)
	_ = f.SetRowHeight(sh, 3, 30)

	// ---- per person ----
	byDay := make(map[string]*DayRow, len(rep.Rows))
	pt := map[uint64]*payTotals{}
	at := func(id uint64) *payTotals {
		if pt[id] == nil {
			pt[id] = &payTotals{}
		}
		return pt[id]
	}
	for i := range rep.Rows {
		r := &rep.Rows[i]
		k := key(r.EmployeeID, r.Date)
		byDay[k] = r
		p := at(r.EmployeeID)
		p.early += r.EarlyMinutes
		if r.AfterShiftMinutes > 0 {
			p.after += r.AfterShiftMinutes
			p.afterDays++
		}
		share := 0.0
		if r.Status == models.StatusLeave {
			share = 1
		} else if r.LeaveHalf != "" {
			share = 0.5
		}
		if isAnnual(r.LeaveCode) {
			p.annual += share
		} else {
			p.leave += share
		}
		if off, isOff := rep.OffDays[k]; isOff && r.CheckInAt != nil {
			if off == "" {
				p.offDays++
			} else {
				p.holidays++
			}
		}
	}
	for _, o := range rep.Overtime {
		if o.Status != models.LeaveApproved {
			continue // payroll pays approved overtime; pending is on the Overtime sheet
		}
		if _, isOff := rep.OffDays[key(o.EmployeeID, o.Date)]; isOff {
			at(o.EmployeeID).otDouble += o.Minutes
		} else {
			at(o.EmployeeID).ot += o.Minutes
		}
	}

	minutesFmt := `#,##0;-#,##0;"—"` // Q-mn: zero shows as a dash
	plain := st.pay("plain", &excelize.Style{})
	name := st.pay("name", &excelize.Style{Alignment: &excelize.Alignment{Horizontal: "left", Vertical: "center", Indent: 1}})
	qmn := st.pay("qmn", &excelize.Style{CustomNumFmt: &minutesFmt})
	absent := st.pay("absent", &excelize.Style{Font: &excelize.Font{Color: payRed}})
	dayStyle := func(kind int, late bool) int {
		s := &excelize.Style{Font: &excelize.Font{Color: "#000000"}, Alignment: &excelize.Alignment{Horizontal: "center", Vertical: "center", ShrinkToFit: true}}
		switch kind {
		case dayAbsent:
			s.Fill = fill(payAbsentFill)
		case dayLeave:
			s.Fill = fill(payLeaveFill)
		case dayOff:
			s.Fill, s.Font.Color = fill(payOffFill), "#6B7280"
		case dayHoliday:
			s.Fill, s.Font.Color = fill(payHolidayFill), "#1E40AF"
		case dayWorkedOff:
			s.Fill = fill(payWorkedFill)
		}
		if late {
			s.Font.Color = payRed
		}
		return st.pay(fmt.Sprintf("day%d%v", kind, late), s)
	}
	hours := func(m int) float64 { return math.Round(float64(m)/60*100) / 100 }

	row := payFirstRow
	for _, s := range rep.Summaries {
		p := at(s.EmployeeID)
		_ = f.SetCellValue(sh, cell(1, row), safeCell(s.EmployeeCode))
		_ = f.SetCellStyle(sh, cell(1, row), cell(1, row), plain)
		_ = f.SetCellValue(sh, cell(2, row), safeCell(s.FullName))
		_ = f.SetCellStyle(sh, cell(2, row), cell(2, row), name)
		for i, day := range days {
			k := day + "#" + itoa(s.EmployeeID)
			off, isOff := rep.OffDays[k]
			text, kind, late := payDay(rep, byDay[k], off, isOff)
			c := cell(3+i, row)
			if text != "" {
				_ = f.SetCellValue(sh, c, safeCell(text))
			}
			_ = f.SetCellStyle(sh, c, c, dayStyle(kind, late))
		}
		vals := []any{s.WorkingDays, s.PresentDays, s.WorkingMinutes, s.LateMinutes, s.LateDays, p.early, s.EarlyLeaveDays,
			p.after, p.afterDays, s.AbsentDays, p.leave, p.annual, p.offDays, p.holidays, hours(p.ot), hours(p.otDouble)}
		for j, v := range vals {
			c := cell(firstSum+j, row)
			_ = f.SetCellValue(sh, c, v)
			style := plain
			switch j {
			case 2, 3, 5, 7: // Q-mn
				style = qmn
			case 9:
				style = absent
			}
			_ = f.SetCellStyle(sh, c, c, style)
		}
		_ = f.SetRowHeight(sh, row, 24)
		row++
	}
	if len(rep.Summaries) == 0 {
		_ = f.SetCellValue(sh, cell(1, row), "No staff in this report.")
		row++
	}

	// ---- legend and notes ----
	row++
	legend := []struct {
		text       string
		kind, span int
		late       bool
	}{
		{"Absent", dayAbsent, 1, false}, {"Leave / AL", dayLeave, 1, false}, {"OFF (day off)", dayOff, 1, false},
		{"Holiday", dayHoliday, 1, false}, {"Worked a day off", dayWorkedOff, 1, false}, {"Late check-in", dayPlain, 1, true},
	}
	_ = f.SetCellValue(sh, cell(2, row), "Colours")
	_ = f.SetCellStyle(sh, cell(2, row), cell(2, row), st.pay("legendLabel", &excelize.Style{Font: &excelize.Font{Bold: true, Color: "#000000"},
		Border: []excelize.Border{}, Alignment: &excelize.Alignment{Horizontal: "right", Vertical: "center"}}))
	for i, l := range legend {
		c := cell(3+i, row)
		_ = f.SetCellValue(sh, c, l.text)
		_ = f.SetCellStyle(sh, c, c, dayStyle(l.kind, l.late))
	}
	_ = f.SetRowHeight(sh, row, 22)
	made := rep.GeneratedAt.In(rep.Loc).Format("02 Jan 2006 15:04")
	if rep.GeneratedBy != "" {
		made += " by " + rep.GeneratedBy
	}
	company := rep.Company
	if company == "" {
		company = "Attendance"
	}
	notes := []string{
		"Q-mn = minutes. Day Work = scheduled working days so far · Attendance = days checked in · Checkout Overtime = minutes checked out after the shift's end · Absent: half a day counts 0.5.",
		"OT Day = days worked on a day off · OT Day x2 = days worked on a public holiday · OT Hours = approved overtime on working days · OT Hours x2 = approved overtime on days off and public holidays.",
		"Pending overtime is not counted here — see the Overtime sheet.",
		fmt.Sprintf("%s · %s · Generated %s · Times in %s", company, rep.Scope, made, rep.Loc.String()),
	}
	note := st.pay("note", &excelize.Style{Font: &excelize.Font{Color: "#475569", Size: 10}, Border: []excelize.Border{},
		Alignment: &excelize.Alignment{Horizontal: "left", Vertical: "center"}})
	for i, n := range notes {
		r := row + 1 + i
		_ = f.SetCellValue(sh, cell(1, r), n)
		_ = f.MergeCell(sh, cell(1, r), cell(lastCol, r))
		_ = f.SetCellStyle(sh, cell(1, r), cell(lastCol, r), note)
	}

	// ---- widths, frozen panes, printing ----
	_ = f.SetColWidth(sh, "A", "A", 11)
	_ = f.SetColWidth(sh, "B", "B", 24)
	_ = f.SetColWidth(sh, colName(3), colName(firstSum-1), 13)
	col = firstSum
	for _, g := range payGroups {
		w := 12.0
		if len(g.subs) == 1 && len(g.title) > 10 {
			w = 13
		}
		for range g.subs {
			_ = f.SetColWidth(sh, colName(col), colName(col), w)
			col++
		}
	}
	_ = f.SetPanes(sh, &excelize.Panes{Freeze: true, XSplit: 2, YSplit: 3, TopLeftCell: "C4", ActivePane: "bottomRight"})
	_ = f.SetDefinedName(&excelize.DefinedName{Name: "_xlnm.Print_Titles", RefersTo: fmt.Sprintf("'%s'!$A:$B,'%s'!$2:$3", sh, sh), Scope: sh})
	return nil
}
