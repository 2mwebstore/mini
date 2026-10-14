package demo

// Runs only when TEST_DATABASE_URL is set (a MySQL user allowed to create databases):
//
//	TEST_DATABASE_URL='root:pass@tcp(127.0.0.1:3306)/' go test ./internal/demo -v

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"attendance/backend/internal/attendance"
	"attendance/backend/internal/auth"
	"attendance/backend/internal/database"
	"attendance/backend/internal/models"
	"attendance/backend/internal/report"
	"attendance/backend/internal/secret"
	"attendance/backend/internal/settings"

	mysqldrv "github.com/go-sql-driver/mysql"
	"gorm.io/gorm"
)

func testDB(t *testing.T) *gorm.DB {
	t.Helper()
	base := os.Getenv("TEST_DATABASE_URL")
	if base == "" {
		t.Skip("TEST_DATABASE_URL not set")
	}
	cfg, err := mysqldrv.ParseDSN(base)
	if err != nil {
		t.Fatal(err)
	}
	rb := make([]byte, 4)
	rand.Read(rb)
	name := "att_demo_test_" + hex.EncodeToString(rb)
	cfg.DBName = ""
	admin, err := sql.Open("mysql", cfg.FormatDSN())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := admin.Exec("CREATE DATABASE " + name + " CHARACTER SET utf8mb4"); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { admin.Exec("DROP DATABASE " + name); admin.Close() })
	cfg.DBName = name
	cfg.ParseTime = true
	if _, err := database.Migrate(context.Background(), cfg.FormatDSN()); err != nil {
		t.Fatal(err)
	}
	db, err := database.Open(cfg.FormatDSN(), false)
	if err != nil {
		t.Fatal(err)
	}
	return db
}

func TestSeed(t *testing.T) {
	db := testDB(t)
	ctx := context.Background()
	box, _ := secret.New(strings.Repeat("j", 40))
	st := settings.NewService(db, box)
	loc, _ := time.LoadLocation("Asia/Phnom_Penh")
	// Wednesday mid-afternoon; Constitution Day (Thu 24 Sep) falls inside the history.
	now := time.Date(2026, 9, 30, 15, 0, 0, 0, loc)
	const pw = "Demo-pass-123"

	sum, err := Seed(ctx, Deps{DB: db, Settings: st}, Options{Password: pw, Now: now})
	if err != nil {
		t.Fatal(err)
	}
	if len(sum.Branches) != 3 || sum.Employees != len(people) || len(sum.Logins) != 6 || sum.AttendanceDays < 250 || sum.LeaveRequests != 10 {
		t.Fatalf("summary: %+v", sum)
	}
	if _, err := Seed(ctx, Deps{DB: db, Settings: st}, Options{Password: pw, Now: now}); !errors.Is(err, ErrAlreadySeeded) {
		t.Fatalf("second run: %v", err)
	}

	// Every login works and carries the intended role; branch admins own their branch.
	authSvc := auth.NewService(db, strings.Repeat("j", 40), time.Hour)
	wantRole := map[string]string{"owner@demo.local": "super_admin", "hr@demo.local": "hr_admin", "main.admin@demo.local": "branch_admin",
		"siemreap.admin@demo.local": "branch_admin", "battambang.admin@demo.local": "branch_admin", "regional@demo.local": "regional_supervisor"}
	for _, l := range sum.Logins {
		res, err := authSvc.Login(ctx, l.Email, pw)
		if err != nil {
			t.Fatalf("login %s: %v", l.Email, err)
		}
		if res.Role != wantRole[l.Email] || res.Employee.Kind != models.KindSystem {
			t.Errorf("%s: role %s kind %s, want %s / SYSTEM", l.Email, res.Role, res.Employee.Kind, wantRole[l.Email])
		}
	}
	// Staff are employees only: no panel logins, no admin roles.
	var staffLogins int64
	db.Model(&models.User{}).Joins("JOIN employees e ON e.id = users.employee_id").Where("e.kind = ?", models.KindStaff).Count(&staffLogins)
	var staffAdmins int64
	db.Model(&models.Employee{}).Where("kind = ? AND role_id NOT IN ?", models.KindStaff, []int{int(models.RoleStaff), int(models.RoleManager)}).Count(&staffAdmins)
	if staffLogins != 0 || staffAdmins != 0 {
		t.Errorf("employees with panel logins %d, with admin roles %d", staffLogins, staffAdmins)
	}
	var branches []models.Branch
	db.Find(&branches)
	for _, b := range branches {
		if b.AdminEmployeeID == nil || !b.HasOffice() {
			t.Errorf("branch %s: admin %v, office %v", b.Code, b.AdminEmployeeID, b.HasOffice())
			continue
		}
		var a models.Employee
		db.First(&a, *b.AdminEmployeeID)
		if a.RoleID != models.RoleBranchAdmin || a.BranchID == nil || *a.BranchID != b.ID || !a.IsSystemUser() {
			t.Errorf("branch %s admin %s: role %d branch %v", b.Code, a.EmployeeCode, a.RoleID, a.BranchID)
		}
	}

	// Attendance rows are internally consistent.
	today := time.Date(2026, 9, 30, 0, 0, 0, 0, time.UTC)
	isHol, _ := attendance.HolidaySet(ctx, db, today.AddDate(0, -2, 0), today.AddDate(0, 2, 0))
	if !isHol(time.Date(2026, 9, 24, 0, 0, 0, 0, time.UTC)) {
		t.Fatal("Constitution Day missing")
	}
	var rows []models.Attendance
	db.Preload("Confirmations").Preload("Photos").Preload("Locations").Find(&rows)
	var staff []models.Employee
	db.Where("kind = ?", models.KindStaff).Find(&staff)
	byID := map[uint64]*models.Employee{}
	ids := []uint64{}
	for i := range staff {
		byID[staff[i].ID] = &staff[i]
		ids = append(ids, staff[i].ID)
	}
	plan, err := attendance.LoadPlanner(ctx, db, ids, today.AddDate(0, -2, 0), today.AddDate(0, 2, 0))
	if err != nil {
		t.Fatal(err)
	}
	var pending, rejected, absent, late, leave, todayRows, overnight int
	for _, a := range rows {
		day := a.WorkDate.Format("2006-01-02")
		planned := plan.Day(byID[a.EmployeeID], a.WorkDate)
		if !planned.Work || isHol(a.WorkDate) {
			t.Errorf("attendance %d on non-working day %s (%+v)", a.ID, day, planned)
		}
		if planned.CrossDay && a.CheckOutAt != nil {
			if a.CheckOutAt.In(loc).Day() == a.WorkDate.Day() {
				t.Errorf("night shift %d %s: check-out %s should be the next morning", a.ID, day, a.CheckOutAt.In(loc))
			}
			overnight++
		}
		if got := attendance.DeriveConfirmation(a.Confirmations); a.Status != models.StatusAbsent && a.Status != models.StatusLeave && got != a.ConfirmationStatus {
			t.Errorf("attendance %d %s: confirmation %s, derived %s", a.ID, day, a.ConfirmationStatus, got)
		}
		switch a.Status {
		case models.StatusLeave:
			leave++
			continue
		case models.StatusAbsent:
			absent++
			continue
		}
		if a.WorkDate.After(today) || a.CheckInAt == nil || a.CheckInAt.After(now) || (a.CheckOutAt != nil && a.CheckOutAt.After(now)) {
			t.Errorf("attendance %d %s: times in the future or missing", a.ID, day)
		}
		if a.WorkDate.Equal(today) {
			todayRows++
		}
		if a.LateMinutes > 0 {
			late++
		}
		want := 1
		if a.CheckOutAt != nil {
			want = 2
		}
		// No photos (the Mini App does not take them); each submission has its own location.
		if len(a.Photos) != 0 || len(a.Locations) != want || len(a.Confirmations) != want {
			t.Errorf("attendance %d: photos %d locations %d confirmations %d, want 0/%d/%d", a.ID, len(a.Photos), len(a.Locations), len(a.Confirmations), want, want)
		}
		for _, c := range a.Confirmations {
			ok := false
			for _, l := range a.Locations {
				ok = ok || (c.LocationID != nil && *c.LocationID == l.ID && l.Type == c.Type)
			}
			if !ok {
				t.Errorf("attendance %d: %s review is not linked to its location", a.ID, c.Type)
			}
		}
		for _, c := range a.Confirmations {
			switch c.Status {
			case models.ConfPending:
				pending++
			case models.ConfRejected:
				rejected++
			}
			if c.Status != models.ConfPending && (c.ReviewedBy == nil || c.ReviewedAt == nil || c.ReviewedAt.After(now)) {
				t.Errorf("confirmation %d reviewed %v at %v", c.ID, c.ReviewedBy, c.ReviewedAt)
			}
		}
	}
	if pending == 0 || rejected != 1 || absent == 0 || late == 0 || leave < 4 || todayRows < 8 || overnight < 10 {
		t.Errorf("variety: pending %d rejected %d absent %d late %d leave %d today %d overnight %d", pending, rejected, absent, late, leave, todayRows, overnight)
	}
	if sum.Shifts != 2 || sum.RosterEntries < 10 || sum.SpecialSchedules != 3 {
		t.Errorf("schedules: %+v", sum)
	}
	var leaveByStatus []struct {
		Status string
		N      int
	}
	db.Model(&models.LeaveRequest{}).Select("status, COUNT(*) AS n").Group("status").Order("status").Scan(&leaveByStatus)
	if fmt.Sprint(leaveByStatus) != "[{APPROVED 4} {CANCELLED 1} {PENDING 4} {REJECTED 1}]" {
		t.Errorf("leave by status: %v", leaveByStatus)
	}
	// Half days take their hours from the shift: the night shift's first half ends at 02:00.
	var halves []models.LeaveRequest
	db.Where("day_type <> ?", models.LeaveFullDay).Order("id").Find(&halves)
	if len(halves) != 2 || halves[0].Days != 0.5 || *halves[0].HalfFrom != "22:00" || *halves[0].HalfTo != "02:00" ||
		halves[1].DayType != models.LeaveSecondHalf || *halves[1].HalfTo == "" {
		t.Errorf("half-day leave: %+v", halves)
	}

	// The owner sees everyone; a branch admin sees only their branch.
	repSvc := report.NewService(db, st)
	repSvc.Now = func() time.Time { return now }
	actor := func(email string) *auth.Actor {
		res, err := authSvc.Login(ctx, email, pw)
		if err != nil {
			t.Fatal(err)
		}
		a, err := authSvc.ActorFromJWT(ctx, res.Token)
		if err != nil {
			t.Fatal(err)
		}
		return a
	}
	from := today.AddDate(0, 0, -27)
	all, err := repSvc.Build(ctx, actor("owner@demo.local"), report.Filter{From: from, To: today})
	if err != nil {
		t.Fatal(err)
	}
	if all.StaffCount != len(people) || all.Totals.PresentDays == 0 || all.Totals.AbsentDays == 0 || all.Totals.LeaveDays == 0 {
		t.Errorf("owner report: staff %d totals %+v", all.StaffCount, all.Totals)
	}
	for _, s := range all.Summaries {
		if s.WorkingDays < 18 { // staff are backdated
			t.Errorf("%s: only %d working days in the report (backdating failed?)", s.EmployeeCode, s.WorkingDays)
		}
	}
	sr, err := repSvc.Build(ctx, actor("siemreap.admin@demo.local"), report.Filter{From: from, To: today})
	if err != nil {
		t.Fatal(err)
	}
	for _, s := range sr.Summaries {
		if s.Branch != "Siem Reap" {
			t.Errorf("Siem Reap admin sees %s (%s)", s.EmployeeCode, s.Branch)
		}
	}
	if sr.StaffCount != 5 {
		t.Errorf("Siem Reap staff count %d", sr.StaffCount)
	}
	reg, err := repSvc.Build(ctx, actor("regional@demo.local"), report.Filter{From: from, To: today})
	if err != nil {
		t.Fatal(err)
	}
	for _, s := range reg.Summaries {
		if s.Branch != "Siem Reap" && s.Branch != "Battambang" {
			t.Errorf("regional supervisor (SR + BB) sees %s (%s)", s.EmployeeCode, s.Branch)
		}
	}
	if reg.StaffCount != 9 { // 5 Siem Reap + 4 Battambang (she is a system user, not staff)
		t.Errorf("regional supervisor staff count %d", reg.StaffCount)
	}
}
