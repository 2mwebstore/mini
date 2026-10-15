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
	if got := f.GetSheetList(); strings.Join(got, ",") != "Summary,Daily attendance,Overtime" {
		t.Fatalf("sheets: %v", got)
	}
	// A title block on every sheet: company, period and filters, who made it.
	if v, _ := f.GetCellValue("Summary", "A1"); !strings.Contains(v, "Demo Co") {
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
	// Summary: KPI cards and the per-person table with its overtime.
	sum, _ := f.GetRows("Summary")
	if !strings.Contains(strings.Join(sum[4], "|"), "OT APPROVED") || !strings.Contains(strings.Join(sum[5], "|"), "1h 30m") {
		t.Fatalf("kpis: %v / %v", sum[4], sum[5])
	}
}

func TestMinutesFormatting(t *testing.T) {
	if got := timeutil.FormatMinutesThousands(2845*60 + 30); got != "2,845h 30m" {
		t.Fatalf("got %s", got)
	}
}
