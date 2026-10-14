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
	data, err := XLSX(sampleReport())
	if err != nil || len(data) < 1000 || string(data[:2]) != "PK" {
		t.Fatalf("xlsx invalid: %v (%d bytes)", err, len(data))
	}
	// Numbers must land under their own headers (columns shift when headers change).
	f, err := excelize.OpenReader(bytes.NewReader(data))
	if err != nil {
		t.Fatal(err)
	}
	rows, _ := f.GetRows("Attendance")
	col := map[string]int{}
	for i, h := range rows[0] {
		col[h] = i
	}
	dara := rows[1]
	if dara[col["Employee Name"]] != "Dara Sok" || dara[col["Branch"]] != "Phnom Penh" || dara[col["Working Minutes"]] != "518" {
		t.Fatalf("cells misplaced: %v", dara)
	}
	if late := rows[2]; late[col["Late Minutes"]] != "25" {
		t.Fatalf("late minutes misplaced: %v", late)
	}
}

func TestMinutesFormatting(t *testing.T) {
	if got := timeutil.FormatMinutesThousands(2845*60 + 30); got != "2,845h 30m" {
		t.Fatalf("got %s", got)
	}
}
