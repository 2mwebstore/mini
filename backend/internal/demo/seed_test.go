package demo

// Runs only when TEST_DATABASE_URL is set (a MySQL user allowed to create databases):
//
//	TEST_DATABASE_URL='root:pass@tcp(127.0.0.1:3306)/' go test ./internal/demo -v

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"sync"
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

type memStore struct {
	mu   sync.Mutex
	objs map[string][]byte
}

func (m *memStore) Put(_ context.Context, key string, data []byte, _ string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.objs[key] = append([]byte(nil), data...)
	return nil
}
func (m *memStore) Get(_ context.Context, key string) (io.ReadCloser, int64, string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	d, ok := m.objs[key]
	if !ok {
		return nil, 0, "", fmt.Errorf("not found")
	}
	return io.NopCloser(bytes.NewReader(d)), int64(len(d)), "image/jpeg", nil
}
func (m *memStore) Ping(context.Context) error { return nil }
func (m *memStore) URI(k string) string        { return "mem://" + k }

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
	store := &memStore{objs: map[string][]byte{}}
	loc, _ := time.LoadLocation("Asia/Phnom_Penh")
	// Wednesday mid-afternoon; Constitution Day (Thu 24 Sep) falls inside the history.
	now := time.Date(2026, 9, 30, 15, 0, 0, 0, loc)
	const pw = "Demo-pass-123"

	sum, err := Seed(ctx, Deps{DB: db, Store: store, Settings: st}, Options{Password: pw, Now: now})
	if err != nil {
		t.Fatal(err)
	}
	if len(sum.Branches) != 3 || sum.Employees != len(people) || len(sum.Logins) != 7 || sum.AttendanceDays < 250 || sum.LeaveRequests != 8 {
		t.Fatalf("summary: %+v", sum)
	}
	if _, err := Seed(ctx, Deps{DB: db, Store: store, Settings: st}, Options{Password: pw, Now: now}); !errors.Is(err, ErrAlreadySeeded) {
		t.Fatalf("second run: %v", err)
	}

	// Every login works and carries the intended role; branch admins own their branch.
	authSvc := auth.NewService(db, strings.Repeat("j", 40), time.Hour)
	wantRole := map[string]string{"owner@demo.local": "super_admin", "hr@demo.local": "hr_admin", "dara.manager@demo.local": "manager",
		"rithy.manager@demo.local": "manager", "main.admin@demo.local": "branch_admin", "siemreap.admin@demo.local": "branch_admin",
		"battambang.admin@demo.local": "branch_admin"}
	for _, l := range sum.Logins {
		res, err := authSvc.Login(ctx, l.Email, pw)
		if err != nil {
			t.Fatalf("login %s: %v", l.Email, err)
		}
		if res.Role != wantRole[l.Email] {
			t.Errorf("%s: role %s, want %s", l.Email, res.Role, wantRole[l.Email])
		}
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
		if a.RoleID != models.RoleBranchAdmin || a.BranchID == nil || *a.BranchID != b.ID {
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
	var pending, rejected, absent, late, leave, todayRows int
	for _, a := range rows {
		day := a.WorkDate.Format("2006-01-02")
		if wd := a.WorkDate.Weekday(); wd == time.Saturday || wd == time.Sunday || isHol(a.WorkDate) {
			t.Errorf("attendance %d on non-working day %s", a.ID, day)
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
		if len(a.Photos) != want || len(a.Locations) != want || len(a.Confirmations) != want {
			t.Errorf("attendance %d: photos %d locations %d confirmations %d, want %d", a.ID, len(a.Photos), len(a.Locations), len(a.Confirmations), want)
		}
		for _, p := range a.Photos {
			data := store.objs[p.StorageKey]
			sum := sha256.Sum256(data)
			if len(data) == 0 || hex.EncodeToString(sum[:]) != p.ContentSHA256 || p.TelegramUserID == 0 {
				t.Errorf("photo %d (%s) not stored correctly", p.ID, p.StorageKey)
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
	if pending == 0 || rejected != 1 || absent == 0 || late == 0 || leave < 4 || todayRows < 8 {
		t.Errorf("variety: pending %d rejected %d absent %d late %d leave %d today %d", pending, rejected, absent, late, leave, todayRows)
	}
	var leaveByStatus []struct {
		Status string
		N      int
	}
	db.Model(&models.LeaveRequest{}).Select("status, COUNT(*) AS n").Group("status").Scan(&leaveByStatus)
	if fmt.Sprint(leaveByStatus) != "[{APPROVED 3} {CANCELLED 1} {PENDING 3} {REJECTED 1}]" {
		t.Errorf("leave by status: %v", leaveByStatus)
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
	if all.StaffCount != len(people)+1 || all.Totals.PresentDays == 0 || all.Totals.AbsentDays == 0 || all.Totals.LeaveDays == 0 {
		t.Errorf("owner report: staff %d totals %+v", all.StaffCount, all.Totals)
	}
	for _, s := range all.Summaries {
		if s.EmployeeCode != "D-OWNER" && s.WorkingDays < 18 { // the owner is new; staff are backdated
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
}
