package report

import (
	"bytes"
	"encoding/csv"
	"fmt"
	"strings"

	"attendance/backend/internal/timeutil"
)

var exportHeader = []string{"Employee ID", "Employee Name", "Branch", "Department", "Date", "Shift", "Check-In", "Check-Out",
	"Working Hours", "Working Minutes", "Late Minutes", "Early Leave Minutes", "Status", "Leave Type",
	"Confirmation Status", "Confirmed By", "Check-In Reason", "Check-Out Reason", "Overtime Approved (min)", "Overtime Pending (min)"}

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
		fmt.Sprint(r.EarlyMinutes), r.Status, r.LeaveType, r.Confirmation, r.ConfirmedBy, r.CheckInReason, r.CheckOutReason,
		fmt.Sprint(r.OvertimeMinutes), fmt.Sprint(r.OvertimePendingMinutes),
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
