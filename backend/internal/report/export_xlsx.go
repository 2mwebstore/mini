package report

import (
	"fmt"
	"strings"

	"attendance/backend/internal/models"
	"attendance/backend/internal/timeutil"

	"github.com/xuri/excelize/v2"
)

// The Excel export is a payroll report like the CRM's: the Payroll sheet (a day-by-day
// check-in – check-out grid and each person's payroll totals; see export_payroll.go),
// then the day-by-day records and the overtime requests — styled tables with a title
// block, filter buttons, banded rows, coloured statuses, frozen headers and totals.

const (
	sheetDaily    = "Daily attendance"
	sheetOvertime = "Overtime"

	colorInk     = "#0F172A"
	colorMuted   = "#64748B"
	colorHeader  = "#1E3A8A" // table headers
	colorBorder  = "#CBD5E1"
	colorTotals  = "#E0E7FF"
	headerRowNum = 5 // tables start under the title block
)

// tone: a status colour (fill, text).
type tone struct{ fill, font string }

var (
	toneGood    = tone{"#DCFCE7", "#166534"}
	toneWarn    = tone{"#FEF3C7", "#92400E"}
	toneBad     = tone{"#FEE2E2", "#991B1B"}
	toneInfo    = tone{"#DBEAFE", "#1E40AF"}
	toneNeutral = tone{"#F1F5F9", "#475569"}
)

// statusLabel / statusTone: day statuses as words and colours.
var statusLabel = map[string]string{
	models.StatusPresent: "Present", models.StatusLate: "Late", models.StatusEarlyLeave: "Early leave", models.StatusAbsent: "Absent",
	models.StatusLeave: "Leave", models.StatusHoliday: "Holiday", models.StatusWeekend: "Day off", DayNotYet: "Not checked in",
}

func statusTone(s string) tone {
	switch s {
	case models.StatusPresent:
		return toneGood
	case models.StatusLate, models.StatusEarlyLeave:
		return toneWarn
	case models.StatusAbsent:
		return toneBad
	case models.StatusLeave:
		return toneInfo
	}
	return toneNeutral
}

func requestTone(s string) tone {
	switch s {
	case models.LeaveApproved:
		return toneGood
	case models.LeavePending:
		return toneWarn
	case models.LeaveRejected:
		return toneBad
	}
	return toneNeutral
}

var reviewLabel = map[string]string{models.ConfPending: "Pending", models.ConfConfirmed: "Confirmed", models.ConfRejected: "Rejected"}

// styles creates each cell style once.
type styles struct {
	f     *excelize.File
	cache map[string]int
}

func (s *styles) get(key string, st *excelize.Style) int {
	if id, ok := s.cache[key]; ok {
		return id
	}
	id, err := s.f.NewStyle(st)
	if err != nil {
		id = 0
	}
	s.cache[key] = id
	return id
}

func border(color string) []excelize.Border {
	return []excelize.Border{
		{Type: "left", Color: color, Style: 1}, {Type: "right", Color: color, Style: 1},
		{Type: "top", Color: color, Style: 1}, {Type: "bottom", Color: color, Style: 1},
	}
}

func (s *styles) title() int {
	return s.get("title", &excelize.Style{Font: &excelize.Font{Bold: true, Size: 18, Color: colorInk}})
}
func (s *styles) subtitle() int {
	return s.get("subtitle", &excelize.Style{Font: &excelize.Font{Size: 11, Color: colorMuted}})
}
func (s *styles) header() int {
	return s.get("header", &excelize.Style{Font: &excelize.Font{Bold: true, Color: "#FFFFFF"},
		Fill: excelize.Fill{Type: "pattern", Color: []string{colorHeader}, Pattern: 1}, Border: border(colorHeader),
		Alignment: &excelize.Alignment{Vertical: "center", WrapText: true}})
}

// cell: a table cell — alignment, an optional status colour, number/date format, bold.
func (s *styles) cell(align string, t *tone, numFmt int, bold bool) int {
	key := fmt.Sprintf("cell|%s|%v|%d|%v", align, t, numFmt, bold)
	st := &excelize.Style{Font: &excelize.Font{Color: colorInk, Bold: bold}, Alignment: &excelize.Alignment{Horizontal: align, Vertical: "center"}, NumFmt: numFmt}
	if t != nil {
		st.Font.Color = t.font
		st.Font.Bold = true
		st.Fill = excelize.Fill{Type: "pattern", Color: []string{t.fill}, Pattern: 1}
	}
	return s.get(key, st)
}
func (s *styles) totals(align string, numFmt int) int {
	return s.get("totals|"+align+fmt.Sprint(numFmt), &excelize.Style{Font: &excelize.Font{Bold: true, Color: colorInk},
		Fill:      excelize.Fill{Type: "pattern", Color: []string{colorTotals}, Pattern: 1},
		Border:    []excelize.Border{{Type: "top", Color: colorHeader, Style: 2}, {Type: "bottom", Color: colorHeader, Style: 2}},
		Alignment: &excelize.Alignment{Horizontal: align}, NumFmt: numFmt})
}

const (
	fmtInt  = 1  // 0
	fmtDec  = 2  // 0.00
	fmtDate = 15 // d-mmm-yy (Excel's built-in date)
)

// titleBlock: the first lines of every sheet.
func titleBlock(rep *Report, title string) [3][]any {
	period := fmt.Sprintf("%s – %s", rep.From.Format("02 Jan 2006"), rep.To.Format("02 Jan 2006"))
	if rep.From.Equal(rep.To) {
		period = rep.From.Format("Monday 02 Jan 2006")
	}
	made := rep.GeneratedAt.In(rep.Loc).Format("02 Jan 2006 15:04")
	if rep.GeneratedBy != "" {
		made += " by " + rep.GeneratedBy
	}
	company := rep.Company
	if company == "" {
		company = "Attendance"
	}
	return [3][]any{
		{company + " — " + title},
		{"Period: " + period + "   ·   " + rep.Scope},
		{"Generated " + made + "   ·   Times in " + rep.Loc.String()},
	}
}

func colName(i int) string { n, _ := excelize.ColumnNumberToName(i); return n }

// XLSX: Payroll (the CRM's layout), Daily attendance, Overtime.
func XLSX(rep *Report) ([]byte, error) {
	f := excelize.NewFile()
	defer f.Close()
	st := &styles{f: f, cache: map[string]int{}}
	_ = f.SetDocProps(&excelize.DocProperties{Title: "Payroll report", Creator: rep.GeneratedBy, Description: rep.Scope})

	f.SetSheetName("Sheet1", sheetPayroll)
	if err := writePayroll(f, st, rep); err != nil {
		return nil, err
	}
	if _, err := f.NewSheet(sheetDaily); err != nil {
		return nil, err
	}
	if err := writeDaily(f, st, rep); err != nil {
		return nil, err
	}
	if _, err := f.NewSheet(sheetOvertime); err != nil {
		return nil, err
	}
	if err := writeOvertime(f, st, rep); err != nil {
		return nil, err
	}
	// Printing: landscape, all columns on one page wide (as many pages tall as needed);
	// the payroll grid on A3.
	for _, sh := range []string{sheetPayroll, sheetDaily, sheetOvertime} {
		landscape, wide, tall, fitToPage, paper := "landscape", 1, 0, true, 9 // 9 = A4
		if sh == sheetPayroll {
			paper = 8 // A3
		}
		_ = f.SetSheetProps(sh, &excelize.SheetPropsOptions{FitToPage: &fitToPage})
		_ = f.SetPageLayout(sh, &excelize.PageLayoutOptions{Size: &paper, Orientation: &landscape, FitToWidth: &wide, FitToHeight: &tall})
	}
	f.SetActiveSheet(0)
	buf, err := f.WriteToBuffer()
	if err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

func writeTitle(f *excelize.File, st *styles, sheet string, rep *Report, title string, lastCol int) {
	tb := titleBlock(rep, title)
	for i, line := range tb {
		cell := fmt.Sprintf("A%d", i+1)
		_ = f.SetCellValue(sheet, cell, line[0])
		style := st.subtitle()
		if i == 0 {
			style = st.title()
		}
		_ = f.SetCellStyle(sheet, cell, cell, style)
		_ = f.MergeCell(sheet, cell, fmt.Sprintf("%s%d", colName(lastCol), i+1))
	}
	_ = f.SetRowHeight(sheet, 1, 30)
}

// ---------- Daily attendance ----------

func writeDaily(f *excelize.File, st *styles, rep *Report) error {
	const sh = sheetDaily
	head := []string{"Date", "Employee ID", "Employee", "Branch", "Department", "Shift", "Check-in", "Check-out", "Worked", "Worked (min)",
		"Late (min)", "Early leave (min)", "Status", "Leave", "Review", "Reviewed by", "Check-in reason", "Check-out reason",
		"OT approved (min)", "OT pending (min)"}
	writeTitle(f, st, sh, rep, "Daily attendance", len(head))
	for i, h := range head {
		_ = f.SetCellValue(sh, fmt.Sprintf("%s%d", colName(i+1), headerRowNum), h)
	}
	_ = f.SetCellStyle(sh, fmt.Sprintf("A%d", headerRowNum), fmt.Sprintf("%s%d", colName(len(head)), headerRowNum), st.header())
	_ = f.SetRowHeight(sh, headerRowNum, 40)
	warn := toneWarn
	for i, r := range rep.Rows {
		row := headerRowNum + 1 + i
		status := statusLabel[r.Status]
		if status == "" {
			status = r.Status
		}
		vals := []any{r.Date, safeCell(r.EmployeeCode), safeCell(r.FullName), safeCell(r.Branch), safeCell(r.Department), shiftText(r),
			dash(timeutil.FormatClock24(r.CheckInAt, rep.Loc)), dash(timeutil.FormatClock24(r.CheckOutAt, rep.Loc)),
			timeutil.FormatMinutes(r.WorkingMinutes), r.WorkingMinutes, r.LateMinutes, r.EarlyMinutes, status, safeCell(r.LeaveType),
			reviewLabel[r.Confirmation], safeCell(r.ConfirmedBy), safeCell(r.CheckInReason), safeCell(r.CheckOutReason),
			r.OvertimeMinutes, r.OvertimePendingMinutes}
		for j, v := range vals {
			c := fmt.Sprintf("%s%d", colName(j+1), row)
			_ = f.SetCellValue(sh, c, v)
			align, numFmt := "left", 0
			var tn *tone
			switch j {
			case 0:
				numFmt = fmtDate
			case 6, 7:
				align = "center"
			case 9, 10, 11, 18, 19:
				align, numFmt = "right", fmtInt
			case 12:
				t := statusTone(r.Status)
				tn, align = &t, "center"
			}
			if (j == 10 && r.LateMinutes > 0) || (j == 11 && r.EarlyMinutes > 0) || (j == 19 && r.OvertimePendingMinutes > 0) {
				tn = &warn
			}
			_ = f.SetCellStyle(sh, c, c, st.cell(align, tn, numFmt, false))
		}
	}
	last := headerRowNum + len(rep.Rows)
	if len(rep.Rows) > 0 {
		_ = f.AddTable(sh, &excelize.Table{Range: fmt.Sprintf("A%d:%s%d", headerRowNum, colName(len(head)), last), Name: "DailyAttendance",
			StyleName: "TableStyleLight9", ShowRowStripes: boolPtr(true)})
	}
	widths := []float64{12, 12, 24, 16, 16, 24, 10, 10, 10, 10, 9, 10, 14, 18, 11, 18, 28, 28, 12, 12}
	for i, w := range widths {
		_ = f.SetColWidth(sh, colName(i+1), colName(i+1), w)
	}
	_ = f.SetPanes(sh, &excelize.Panes{Freeze: true, XSplit: 3, YSplit: headerRowNum, TopLeftCell: fmt.Sprintf("D%d", headerRowNum+1), ActivePane: "bottomRight"})
	return nil
}

// ---------- Overtime ----------

func writeOvertime(f *excelize.File, st *styles, rep *Report) error {
	const sh = sheetOvertime
	head := []string{"Date", "Employee ID", "Employee", "Branch", "Department", "From", "To", "Length", "Minutes", "Hours", "Reason",
		"Status", "Reviewed by", "Reviewed at"}
	writeTitle(f, st, sh, rep, "Overtime (approved and pending)", len(head))
	for i, h := range head {
		_ = f.SetCellValue(sh, fmt.Sprintf("%s%d", colName(i+1), headerRowNum), h)
	}
	_ = f.SetCellStyle(sh, fmt.Sprintf("A%d", headerRowNum), fmt.Sprintf("%s%d", colName(len(head)), headerRowNum), st.header())
	_ = f.SetRowHeight(sh, headerRowNum, 28)
	approved, pending := 0, 0
	for i, o := range rep.Overtime {
		row := headerRowNum + 1 + i
		to := o.EndAt.In(rep.Loc).Format("15:04")
		if o.EndAt.In(rep.Loc).Format("2006-01-02") != o.Date.Format("2006-01-02") {
			to += " (+1)"
		}
		reviewedAt := ""
		if o.ReviewedAt != nil {
			reviewedAt = o.ReviewedAt.In(rep.Loc).Format("02 Jan 2006 15:04")
		}
		status := strings.ToUpper(o.Status[:1]) + strings.ToLower(o.Status[1:])
		vals := []any{o.Date, safeCell(o.EmployeeCode), safeCell(o.FullName), safeCell(o.Branch), safeCell(o.Department),
			o.StartAt.In(rep.Loc).Format("15:04"), to, timeutil.FormatMinutes(o.Minutes), o.Minutes, float64(o.Minutes) / 60,
			safeCell(o.Reason), status, safeCell(o.ReviewedBy), reviewedAt}
		for j, v := range vals {
			c := fmt.Sprintf("%s%d", colName(j+1), row)
			_ = f.SetCellValue(sh, c, v)
			align, numFmt := "left", 0
			var tn *tone
			switch j {
			case 0:
				numFmt = fmtDate
			case 5, 6:
				align = "center"
			case 8:
				align, numFmt = "right", fmtInt
			case 9:
				align, numFmt = "right", fmtDec
			case 11:
				t := requestTone(o.Status)
				tn, align = &t, "center"
			}
			_ = f.SetCellStyle(sh, c, c, st.cell(align, tn, numFmt, false))
		}
		if o.Status == models.LeaveApproved {
			approved += o.Minutes
		} else {
			pending += o.Minutes
		}
	}
	last := headerRowNum + len(rep.Overtime)
	if len(rep.Overtime) > 0 {
		_ = f.AddTable(sh, &excelize.Table{Range: fmt.Sprintf("A%d:%s%d", headerRowNum, colName(len(head)), last), Name: "OvertimeRequests",
			StyleName: "TableStyleLight9", ShowRowStripes: boolPtr(true)})
	} else {
		_ = f.SetCellValue(sh, fmt.Sprintf("A%d", headerRowNum+1), "No approved or pending overtime in this period.")
		_ = f.SetCellStyle(sh, fmt.Sprintf("A%d", headerRowNum+1), fmt.Sprintf("A%d", headerRowNum+1), st.subtitle())
	}
	// totals: approved and pending
	for k, line := range []struct {
		label string
		mins  int
	}{{"APPROVED", approved}, {"PENDING", pending}, {"TOTAL", approved + pending}} {
		r := last + 2 + k
		if len(rep.Overtime) == 0 {
			r++
		}
		vals := map[string]any{"A": line.label, "H": timeutil.FormatMinutes(line.mins), "I": line.mins, "J": float64(line.mins) / 60}
		for col := 1; col <= len(head); col++ {
			c := fmt.Sprintf("%s%d", colName(col), r)
			if v, ok := vals[colName(col)]; ok {
				_ = f.SetCellValue(sh, c, v)
			}
			align, numFmt := "left", 0
			if col == 9 {
				align, numFmt = "right", fmtInt
			} else if col == 10 {
				align, numFmt = "right", fmtDec
			}
			_ = f.SetCellStyle(sh, c, c, st.totals(align, numFmt))
		}
	}
	widths := []float64{12, 12, 24, 16, 16, 9, 11, 10, 9, 8, 36, 11, 18, 18}
	for i, w := range widths {
		_ = f.SetColWidth(sh, colName(i+1), colName(i+1), w)
	}
	_ = f.SetPanes(sh, &excelize.Panes{Freeze: true, XSplit: 3, YSplit: headerRowNum, TopLeftCell: fmt.Sprintf("D%d", headerRowNum+1), ActivePane: "bottomRight"})
	return nil
}

func boolPtr(b bool) *bool { return &b }
