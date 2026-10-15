package report

import (
	"bytes"
	"strings"
	"testing"
	"time"

	"github.com/xuri/excelize/v2"

	"attendance/backend/internal/models"
	"attendance/backend/internal/timeutil"
)

func sampleReport() *Report {
	loc := timeutil.LoadLocation("Asia/Phnom_Penh")
	d := time.Date(2026, 10, 8, 0, 0, 0, 0, time.UTC)
	in, _ := timeutil.At(d, "08:52", loc)
	out, _ := timeutil.At(d, "17:30", loc)
	late, _ := timeutil.At(d, "09:25", loc)
	return &Report{From: d, To: d, Loc: loc, Rows: []DayRow{
		{EmployeeCode: "GT001", FullName: "Dara Sok", Branch: "Phnom Penh", Date: d, CheckInAt: &in, CheckOutAt: &out, WorkingMinutes: 518, Status: models.StatusPresent, Confirmation: models.ConfConfirmed},
		{EmployeeCode: "GT002", FullName: "=HYPERLINK(\"x\")", Date: d, CheckInAt: &late, LateMinutes: 25, Status: models.StatusLate, Confirmation: models.ConfPending},
		{EmployeeCode: "GT003", FullName: "Chan", Date: d, Status: models.StatusAbsent},
		{EmployeeCode: "GT004", FullName: "Vanna", Date: d, Status: models.StatusLeave, LeaveType: "Annual Leave", Confirmation: models.ConfConfirmed},
	}}
}

func TestDayText(t *testing.T) {
	txt := DayText(sampleReport(), "DAILY ATTENDANCE REPORT", true)
	for _, want := range []string{"Total Staff: 4", "Confirmed: 1", "Pending: 1", "Leave: 1", "Absent: 1", "Late: 1",
		"08:52 → 17:30 ✅", "Chan - No check-in", "Late 25 minutes", "Pending confirmation", "NEED ATTENTION"} {
		if !strings.Contains(txt, want) {
			t.Errorf("daily text missing %q\n%s", want, txt)
		}
	}
	if !strings.Contains(txt, "=HYPERLINK(&#34;x&#34;)") {
		t.Error("names must be HTML-escaped")
	}
}

func TestCSVInjectionAndBOM(t *testing.T) {
	data, err := CSV(sampleReport())
	if err != nil {
		t.Fatal(err)
	}
	s := string(data)
	if !strings.HasPrefix(s, "\xEF\xBB\xBF") {
		t.Error("missing UTF-8 BOM")
	}
	if !strings.Contains(s, `'=HYPERLINK`) {
		t.Error("formula cells must be neutralised")
	}
	if !strings.Contains(s, "Employee ID,Employee Name,Branch,Department,Date,Shift,Check-In,Check-Out") {
		t.Error("unexpected header")
	}
}

func TestXLSX(t *testing.T) {
	rep := sampleReport()
	rep.Company, rep.GeneratedBy, rep.Scope = "Demo Co", "Owner", "Branch: Phnom Penh"
	rep.Summaries = []Summary{{EmployeeCode: "GT001", FullName: "Dara Sok", Branch: "Phnom Penh", WorkingDays: 1, PresentDays: 1, WorkingMinutes: 518, OvertimeMinutes: 90, OvertimePendingMinutes: 60}}
	rep.Totals, rep.StaffCount = rep.Summaries[0], 1
	start, _ := timeutil.At(rep.From, "17:30", rep.Loc)
	rep.Overtime = []OvertimeRow{
		{EmployeeCode: "GT001", FullName: "Dara Sok", Date: rep.From, StartAt: start, EndAt: start.Add(90 * time.Minute), Minutes: 90, Reason: "Month-end", Status: models.LeaveApproved, ReviewedBy: "Mona"},
		{EmployeeCode: "GT001", FullName: "Dara Sok", Date: rep.From, StartAt: start.Add(5 * time.Hour), EndAt: start.Add(6 * time.Hour), Minutes: 60, Reason: "Server move", Status: models.LeavePending},
	}
	data, err := XLSX(rep)
	if err != nil || len(data) < 1000 || string(data[:2]) != "PK" {
		t.Fatalf("xlsx invalid: %v (%d bytes)", err, len(data))
	}
	f, err := excelize.OpenReader(bytes.NewReader(data))
	if err != nil {
		t.Fatal(err)
	}
	if got := f.GetSheetList(); strings.Join(got, ",") != "Payroll,Daily attendance,Overtime" {
		t.Fatalf("sheets: %v", got)
	}
	if v, _ := f.GetCellValue("Payroll", "A1"); v != "Detail Report Payroll 08 October 2026" {
		t.Fatalf("payroll title: %q", v)
	}
	// A title block on the detail sheets: company, period and filters, who made it.
	if v, _ := f.GetCellValue("Daily attendance", "A1"); !strings.Contains(v, "Demo Co") {
		t.Fatalf("title: %q", v)
	}
	if v, _ := f.GetCellValue("Daily attendance", "A2"); !strings.Contains(v, "08 Oct 2026") || !strings.Contains(v, "Branch: Phnom Penh") {
		t.Fatalf("period line: %q", v)
	}
	// Numbers land under their own headers (row 5 is the table header).
	rows, _ := f.GetRows("Daily attendance")
	col := map[string]int{}
	for i, h := range rows[4] {
		col[h] = i
	}
	dara := rows[5]
	if dara[col["Employee"]] != "Dara Sok" || dara[col["Branch"]] != "Phnom Penh" || dara[col["Worked (min)"]] != "518" || dara[col["Status"]] != "Present" {
		t.Fatalf("cells misplaced: %v", dara)
	}
	if late := rows[6]; late[col["Late (min)"]] != "25" || late[col["Status"]] != "Late" || !strings.HasPrefix(late[col["Employee"]], "'=") {
		t.Fatalf("late row: %v", late)
	}
	// Overtime: both requests, and the approved / pending totals.
	ot, _ := f.GetRows("Overtime")
	flat := strings.Join(func() []string {
		var out []string
		for _, r := range ot {
			out = append(out, strings.Join(r, "|"))
		}
		return out
	}(), "\n")
	for _, want := range []string{"Month-end", "Approved", "Server move", "Pending", "APPROVED|||||||1h 30m|90", "PENDING|||||||1h 00m|60", "TOTAL|||||||2h 30m|150"} {
		if !strings.Contains(flat, want) {
			t.Errorf("overtime sheet missing %q:\n%s", want, flat)
		}
	}
}

// The Payroll sheet: the CRM's layout — a day grid, then each person's payroll totals.
func TestPayrollSheet(t *testing.T) {
	loc := timeutil.LoadLocation("Asia/Phnom_Penh")
	day := func(n int) time.Time { return time.Date(2026, 10, n, 0, 0, 0, 0, time.UTC) }
	at := func(n int, hm string) *time.Time { v, _ := timeutil.At(day(n), hm, loc); return &v }
	rep := &Report{From: day(1), To: day(4), Today: day(15), Loc: loc, Company: "Demo Co", Scope: "Everyone you can see", OffDays: map[string]string{
		key(1, day(3)): "", key(1, day(4)): "", // Dara's weekend (worked the Saturday)
		key(2, day(3)): "Pchum Ben", key(2, day(4)): "",
	}}
	rep.Rows = []DayRow{
		{EmployeeID: 1, EmployeeCode: "GT001", FullName: "Dara Sok", Date: day(1), CheckInAt: at(1, "08:52"), CheckOutAt: at(1, "17:30"), WorkingMinutes: 518, Status: models.StatusPresent},
		{EmployeeID: 1, EmployeeCode: "GT001", FullName: "Dara Sok", Date: day(2), CheckInAt: at(2, "09:25"), CheckOutAt: at(2, "18:10"), WorkingMinutes: 525, LateMinutes: 25, AfterShiftMinutes: 40, Status: models.StatusLate},
		{EmployeeID: 1, EmployeeCode: "GT001", FullName: "Dara Sok", Date: day(3), CheckInAt: at(3, "08:00"), CheckOutAt: at(3, "12:00"), WorkingMinutes: 240, Status: models.StatusPresent},
		{EmployeeID: 2, EmployeeCode: "GT002", FullName: "Chan", Date: day(1), Status: models.StatusAbsent, LeaveType: "Sick Leave (first half)", LeaveHalf: models.LeaveFirstHalf, LeaveCode: "SICK"},
		{EmployeeID: 2, EmployeeCode: "GT002", FullName: "Chan", Date: day(2), Status: models.StatusLeave, LeaveType: "Annual Leave", LeaveCode: "ANNUAL"},
	}
	rep.Summaries = []Summary{
		{EmployeeID: 1, EmployeeCode: "GT001", FullName: "Dara Sok", WorkingDays: 2, PresentDays: 3, WorkingMinutes: 1283, LateDays: 1, LateMinutes: 25},
		{EmployeeID: 2, EmployeeCode: "GT002", FullName: "Chan", WorkingDays: 2, AbsentDays: 0.5, LeaveDays: 1.5},
	}
	rep.Overtime = []OvertimeRow{
		{EmployeeID: 1, Date: day(1), Minutes: 90, Status: models.LeaveApproved},
		{EmployeeID: 1, Date: day(3), Minutes: 120, Status: models.LeaveApproved}, // on a day off: x2
		{EmployeeID: 1, Date: day(2), Minutes: 60, Status: models.LeavePending},   // not paid yet
	}
	data, err := XLSX(rep)
	if err != nil {
		t.Fatal(err)
	}
	f, err := excelize.OpenReader(bytes.NewReader(data))
	if err != nil {
		t.Fatal(err)
	}
	const sh = "Payroll"
	if v, _ := f.GetCellValue(sh, "A1"); v != "Detail Report Payroll October 2026" {
		t.Fatalf("title: %q", v)
	}
	if v, _ := f.GetCellValue(sh, "C2"); v != "01" {
		t.Fatalf("first day heading: %q", v)
	}
	rows, _ := f.GetRows(sh, excelize.Options{RawCellValue: true})
	join := func(r []string) string { return strings.Join(r, "|") }
	// A ID, B Name, C–F the days, then Day Work … OT Hours x2.
	if got := join(rows[1][6:]); got != "Day Work|Attendance|Work Time|Checkin Late||Checkout Early||Checkout Overtime||Absent|Other||||Overtime" {
		t.Fatalf("groups: %s", got)
	}
	if got := join(rows[2][2:]); got != "Check in - Check out||||Count|Count|Q-mn|Q-mn|Count|Q-mn|Count|Q-mn|Count|Absent|Leave|AL|OT Day|OT Day x2|OT Hours|OT Hours x2" {
		t.Fatalf("sub-headings: %s", got)
	}
	if got := join(rows[3]); got != "GT001|Dara Sok|08:52 - 17:30|09:25 - 18:10|08:00 - 12:00|OFF|2|3|1283|25|1|0|0|40|1|0|0|0|1|0|1.5|2" {
		t.Fatalf("Dara: %s", got)
	}
	if got := join(rows[4]); got != "GT002|Chan|½ Sick Leave|AL|Holiday|OFF|2|0|0|0|0|0|0|0|0|0.5|0.5|1|0|0|0|0" {
		t.Fatalf("Chan: %s", got)
	}
	// Colours: absent pink, leave amber, worked a day off green, late check-in red.
	fillOf := func(c string) string {
		id, _ := f.GetCellStyle(sh, c)
		s, _ := f.GetStyle(id)
		if s == nil || len(s.Fill.Color) == 0 {
			return ""
		}
		return strings.ToUpper(strings.TrimPrefix(s.Fill.Color[0], "#"))
	}
	for c, want := range map[string]string{"C5": "F4CCCC", "D5": "FCE8B2", "E4": "D9EAD3", "C4": ""} {
		if got := fillOf(c); got != want {
			t.Errorf("%s fill %q, want %q", c, got, want)
		}
	}
	if id, _ := f.GetCellStyle(sh, "D4"); func() string { s, _ := f.GetStyle(id); return strings.ToUpper(strings.TrimPrefix(s.Font.Color, "#")) }() != "CA3A31" {
		t.Error("a late check-in should be red")
	}
}

func TestHalfDayAbsence(t *testing.T) {
	var s Summary
	accumulate(&s, DayRow{Status: models.StatusAbsent, LeaveHalf: models.LeaveSecondHalf})
	accumulate(&s, DayRow{Status: models.StatusAbsent})
	if s.AbsentDays != 1.5 || s.LeaveDays != 0.5 {
		t.Fatalf("absent %v leave %v", s.AbsentDays, s.LeaveDays)
	}
}

func TestMinutesFormatting(t *testing.T) {
	if got := timeutil.FormatMinutesThousands(2845*60 + 30); got != "2,845h 30m" {
		t.Fatalf("got %s", got)
	}
}
