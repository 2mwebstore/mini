package report

import (
	"bytes"
	"encoding/csv"
	"fmt"
	"strings"

	"attendance/backend/internal/timeutil"

	"github.com/xuri/excelize/v2"
)

var exportHeader = []string{"Employee ID", "Employee Name", "Branch", "Department", "Date", "Shift", "Check-In", "Check-Out",
	"Working Hours", "Working Minutes", "Late Minutes", "Early Leave Minutes", "Status", "Leave Type",
	"Confirmation Status", "Confirmed By"}

// exportCol returns a column's index in exportHeader (so cell positions follow the header).
func exportCol(name string) int {
	for i, h := range exportHeader {
		if h == name {
			return i
		}
	}
	panic("report: unknown export column " + name)
}

func exportRow(rep *Report, r DayRow) []string {
	return []string{
		r.EmployeeCode, r.FullName, r.Branch, r.Department, r.Date.Format("2006-01-02"), shiftText(r),
		dash(timeutil.FormatClock24(r.CheckInAt, rep.Loc)), dash(timeutil.FormatClock24(r.CheckOutAt, rep.Loc)),
		timeutil.FormatMinutes(r.WorkingMinutes), fmt.Sprint(r.WorkingMinutes), fmt.Sprint(r.LateMinutes),
		fmt.Sprint(r.EarlyMinutes), r.Status, r.LeaveType, r.Confirmation, r.ConfirmedBy,
	}
}

// shiftText: "Night Shift 22:00–06:00 (+1)", or "" on a day off.
func shiftText(r DayRow) string {
	if r.ShiftStart == "" {
		return ""
	}
	t := fmt.Sprintf("%s %s–%s", r.Shift, r.ShiftStart, r.ShiftEnd)
	if r.CrossDay {
		t += " (+1)"
	}
	return strings.TrimSpace(t)
}

func dash(s string) string {
	if s == "—" {
		return ""
	}
	return s
}

// safeCell neutralises spreadsheet formula injection from user-controlled text.
func safeCell(s string) string {
	if s != "" && strings.ContainsRune("=+-@\t\r", rune(s[0])) {
		return "'" + s
	}
	return s
}

func CSV(rep *Report) ([]byte, error) {
	var buf bytes.Buffer
	buf.WriteString("\xEF\xBB\xBF") // BOM so Excel opens UTF-8 (Khmer names) correctly
	w := csv.NewWriter(&buf)
	_ = w.Write(exportHeader)
	for _, r := range rep.Rows {
		row := exportRow(rep, r)
		for i := range row {
			row[i] = safeCell(row[i])
		}
		if err := w.Write(row); err != nil {
			return nil, err
		}
	}
	w.Flush()
	return buf.Bytes(), w.Error()
}

func XLSX(rep *Report) ([]byte, error) {
	f := excelize.NewFile()
	defer f.Close()

	const daily = "Attendance"
	f.SetSheetName("Sheet1", daily)
	sw, err := f.NewStreamWriter(daily)
	if err != nil {
		return nil, err
	}
	bold, _ := f.NewStyle(&excelize.Style{Font: &excelize.Font{Bold: true}, Fill: excelize.Fill{Type: "pattern", Color: []string{"#E2E8F0"}, Pattern: 1}})
	_ = sw.SetColWidth(1, 1, 12)
	_ = sw.SetColWidth(2, 2, 24)
	_ = sw.SetColWidth(3, 4, 18)
	_ = sw.SetColWidth(5, len(exportHeader), 14)
	iWork, iLate, iEarly := exportCol("Working Minutes"), exportCol("Late Minutes"), exportCol("Early Leave Minutes")
	head := make([]any, len(exportHeader))
	for i, h := range exportHeader {
		head[i] = excelize.Cell{Value: h, StyleID: bold}
	}
	if err := sw.SetRow("A1", head); err != nil {
		return nil, err
	}
	for i, r := range rep.Rows {
		vals := exportRow(rep, r)
		row := make([]any, len(vals))
		for j, v := range vals {
			row[j] = safeCell(v)
		}
		// numeric columns as numbers
		row[iWork], row[iLate], row[iEarly] = r.WorkingMinutes, r.LateMinutes, r.EarlyMinutes
		cell, _ := excelize.CoordinatesToCellName(1, i+2)
		if err := sw.SetRow(cell, row); err != nil {
			return nil, err
		}
	}
	if err := sw.Flush(); err != nil {
		return nil, err
	}

	const summary = "Summary"
	if _, err := f.NewSheet(summary); err != nil {
		return nil, err
	}
	sh := []string{"Employee ID", "Employee Name", "Branch", "Department", "Working Days", "Present", "Late Days", "Late Minutes",
		"Early Leave Days", "Absent", "Leave", "Pending", "Rejected", "Missing Check-Out", "Working Hours", "Working Minutes", "Avg / Day"}
	for i, h := range sh {
		c, _ := excelize.CoordinatesToCellName(i+1, 1)
		_ = f.SetCellValue(summary, c, h)
	}
	last, _ := excelize.CoordinatesToCellName(len(sh), 1)
	_ = f.SetCellStyle(summary, "A1", last, bold)
	_ = f.SetColWidth(summary, "B", "B", 24)
	_ = f.SetColWidth(summary, "C", "D", 18)
	for i, s := range rep.Summaries {
		vals := []any{safeCell(s.EmployeeCode), safeCell(s.FullName), safeCell(s.Branch), safeCell(s.Department), s.WorkingDays, s.PresentDays, s.LateDays, s.LateMinutes,
			s.EarlyLeaveDays, s.AbsentDays, s.LeaveDays, s.PendingDays, s.RejectedDays, s.MissingCheckout,
			timeutil.FormatMinutes(s.WorkingMinutes), s.WorkingMinutes, timeutil.FormatMinutes(s.AvgMinutes)}
		for j, v := range vals {
			c, _ := excelize.CoordinatesToCellName(j+1, i+2)
			_ = f.SetCellValue(summary, c, v)
		}
	}
	buf, err := f.WriteToBuffer()
	if err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}
