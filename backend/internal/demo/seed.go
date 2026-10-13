// Package demo fills a development database with realistic sample data so the
// owner can explore the admin panel: three branches with branch admins,
// managers and staff, four weeks of attendance (photos, GPS, reviews — some
// late, absent, pending or rejected), leave requests and public holidays.
//
// People and branches are created through the normal services (validation and
// audit log included); attendance and leave rows are written directly so no
// Telegram notifications go out. Not for production databases.
package demo

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"image"
	"image/color"
	"image/jpeg"
	"math/rand/v2"
	"time"

	"attendance/backend/internal/attendance"
	"attendance/backend/internal/audit"
	"attendance/backend/internal/auth"
	"attendance/backend/internal/branch"
	"attendance/backend/internal/employee"
	"attendance/backend/internal/models"
	"attendance/backend/internal/settings"
	"attendance/backend/internal/storage"
	"attendance/backend/internal/timeutil"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// ErrAlreadySeeded: this database already has the demo people (codes D-…).
var ErrAlreadySeeded = errors.New("demo data is already in this database")

const codePrefix = "D-"

type Deps struct {
	DB       *gorm.DB
	Store    storage.Store
	Settings *settings.Service
}

type Options struct {
	Password string    // for every demo admin panel login (min 10 characters)
	Now      time.Time // "today" for the generated history (default: now)
	Days     int       // days of history before today (default 27)
}

type Login struct {
	Email, Name, Role string
	Existing          bool // an owner that was already in the database
}

type Summary struct {
	Branches       []string
	Employees      int
	AttendanceDays int
	LeaveRequests  int
	Holidays       int
	Logins         []Login
}

type branchDef struct {
	code, name, address string
	lat, lng, radius    float64
}

var branchDefs = []branchDef{
	{"MAIN", "", "", 11.5564, 104.9282, 150}, // created by migration 0003; name kept
	{"SR", "Siem Reap", "Sivatha Blvd, Siem Reap", 13.3633, 103.8564, 200},
	{"BB", "Battambang", "Street 1, Battambang", 13.0957, 103.2022, 200},
}

type person struct {
	code, name, role, dept, position string
	branch                           string // branch code; "" = company-wide
	email                            string // admin panel login; "" = none
	branchAdmin                      bool
}

var people = []person{
	{"D-HR01", "Chenda Lim", "hr_admin", "Human Resources", "HR Manager", "", "hr@demo.local", false},

	{"D-PP01", "Sophea Chan", "staff", "Operations", "Branch Manager", "MAIN", "main.admin@demo.local", true},
	{"D-PP02", "Dara Sok", "manager", "Sales", "Sales Supervisor", "MAIN", "dara.manager@demo.local", false},
	{"D-PP03", "Bopha Keo", "staff", "Sales", "Sales Executive", "MAIN", "", false},
	{"D-PP04", "Vuthy Ly", "staff", "Sales", "Sales Executive", "MAIN", "", false},
	{"D-PP05", "Sreymom Phan", "staff", "Finance", "Accountant", "MAIN", "", false},
	{"D-PP06", "Piseth Long", "staff", "Development", "Software Developer", "MAIN", "", false},
	{"D-PP07", "Kanha Mao", "staff", "Customer Service", "Customer Support", "MAIN", "", false},

	{"D-SR01", "Vanna Kim", "staff", "Operations", "Branch Manager", "SR", "siemreap.admin@demo.local", true},
	{"D-SR02", "Rithy Heng", "manager", "Operations", "Front Office Supervisor", "SR", "rithy.manager@demo.local", false},
	{"D-SR03", "Sothea Nhem", "staff", "Customer Service", "Receptionist", "SR", "", false},
	{"D-SR04", "Kosal Pen", "staff", "Operations", "Driver", "SR", "", false},
	{"D-SR05", "Nita Ros", "staff", "Sales", "Sales Executive", "SR", "", false},

	{"D-BB01", "Malis Ouk", "staff", "Operations", "Branch Manager", "BB", "battambang.admin@demo.local", true},
	{"D-BB02", "Narin Seng", "staff", "Sales", "Sales Executive", "BB", "", false},
	{"D-BB03", "Phalla Touch", "staff", "Finance", "Cashier", "BB", "", false},
	{"D-BB04", "Ratha Chhim", "staff", "Operations", "Warehouse Officer", "BB", "", false},
}

// Fixed-date public holidays in Cambodia (lunar-calendar ones vary by year).
var holidayDefs = []struct {
	month time.Month
	day   int
	name  string
}{
	{time.January, 1, "International New Year Day"}, {time.January, 7, "Victory over Genocide Day"},
	{time.March, 8, "International Women's Day"}, {time.April, 14, "Khmer New Year"}, {time.April, 15, "Khmer New Year"},
	{time.April, 16, "Khmer New Year"}, {time.May, 1, "International Labour Day"}, {time.May, 14, "King's Birthday"},
	{time.June, 18, "Queen Mother's Birthday"}, {time.September, 24, "Constitution Day"},
	{time.October, 15, "Commemoration Day of the King Father"}, {time.October, 29, "King's Coronation Day"},
	{time.November, 9, "Independence Day"},
}

type seeder struct {
	ctx     context.Context
	d       Deps
	o       Options
	cli     *auth.Actor
	loc     *time.Location
	today   time.Time
	rng     *rand.Rand
	emps    map[string]*models.Employee // by code
	branch  map[string]*models.Branch   // by code
	owner   *models.Employee
	isHol   func(time.Time) bool
	photoNo uint64
	sum     *Summary
}

func Seed(ctx context.Context, d Deps, o Options) (*Summary, error) {
	if len(o.Password) < 10 {
		return nil, fmt.Errorf("demo password must be at least 10 characters")
	}
	if o.Now.IsZero() {
		o.Now = time.Now()
	}
	if o.Days <= 0 {
		o.Days = 27
	}
	var n int64
	if err := d.DB.WithContext(ctx).Model(&models.Employee{}).Where("employee_code LIKE ?", codePrefix+"%").Count(&n).Error; err != nil {
		return nil, err
	}
	if n > 0 {
		return nil, ErrAlreadySeeded
	}
	st, err := d.Settings.Get(ctx)
	if err != nil {
		return nil, err
	}
	s := &seeder{
		ctx: ctx, d: d, o: o, loc: st.Location, today: timeutil.DateOf(o.Now, st.Location),
		cli:  &auth.Actor{Employee: models.Employee{RoleID: models.RoleSuperAdmin, FullName: "Demo seed"}, Channel: auth.ChannelCLI},
		rng:  rand.New(rand.NewPCG(2026, 10)), // fixed: the same demo every time
		emps: map[string]*models.Employee{}, branch: map[string]*models.Branch{}, sum: &Summary{},
	}
	steps := []func() error{s.ensureOwner, s.branches, s.staff, s.backdate, s.holidays, s.leave, s.attendance, s.audit}
	for _, step := range steps {
		if err := step(); err != nil {
			return nil, err
		}
	}
	return s.sum, nil
}

// ---------- people and branches ----------

func (s *seeder) ensureOwner() error {
	var owner models.Employee
	err := s.d.DB.WithContext(s.ctx).Where("role_id = ? AND deleted_at IS NULL", models.RoleSuperAdmin).Order("id").First(&owner).Error
	if err == nil {
		s.owner = &owner
		var u models.User
		email := "(no admin panel login — create one with: api create-admin)"
		if s.d.DB.WithContext(s.ctx).Where("employee_id = ?", owner.ID).First(&u).Error == nil {
			email = u.Email
		}
		s.sum.Logins = append(s.sum.Logins, Login{Email: email, Name: owner.FullName, Role: "Owner (super admin)", Existing: true})
		return nil
	}
	if !errors.Is(err, gorm.ErrRecordNotFound) {
		return err
	}
	svc := employee.NewService(s.d.DB)
	e, err := svc.Create(s.ctx, s.cli, employee.Input{EmployeeCode: "D-OWNER", FullName: "Demo Owner", Role: "super_admin"})
	if err != nil {
		return fmt.Errorf("create owner: %w", err)
	}
	if _, err := svc.CreateUser(s.ctx, s.cli, e.ID, "owner@demo.local", s.o.Password); err != nil {
		return fmt.Errorf("owner login: %w", err)
	}
	s.owner = e
	s.sum.Logins = append(s.sum.Logins, Login{Email: "owner@demo.local", Name: e.FullName, Role: "Owner (super admin)"})
	return nil
}

func (s *seeder) branches() error {
	svc := branch.NewService(s.d.DB)
	for _, def := range branchDefs {
		var b models.Branch
		err := s.d.DB.WithContext(s.ctx).Where("code = ?", def.code).First(&b).Error
		if errors.Is(err, gorm.ErrRecordNotFound) {
			code, name, addr := def.code, def.name, def.address
			nb, err := svc.Create(s.ctx, s.cli, branch.Input{Code: &code, Name: &name, Address: &addr})
			if err != nil {
				return fmt.Errorf("create branch %s: %w", def.code, err)
			}
			b = *nb
		} else if err != nil {
			return err
		}
		if !b.HasOffice() {
			lat, lng, r := def.lat, def.lng, def.radius
			nb, err := svc.Update(s.ctx, s.cli, b.ID, branch.Input{OfficeLatitude: &lat, OfficeLongitude: &lng, AllowedRadiusMeters: &r})
			if err != nil {
				return fmt.Errorf("branch %s office: %w", def.code, err)
			}
			b = *nb
		}
		bb := b
		s.branch[def.code] = &bb
		s.sum.Branches = append(s.sum.Branches, b.Name)
	}
	return nil
}

func (s *seeder) staff() error {
	empSvc := employee.NewService(s.d.DB)
	brSvc := branch.NewService(s.d.DB)
	managers := map[string]uint64{} // branch code -> manager employee id
	for i, p := range people {
		in := employee.Input{EmployeeCode: p.code, FullName: p.name, Role: p.role, Department: p.dept, Position: ptr(p.position),
			Email: ptr(p.email), Phone: ptr(fmt.Sprintf("+855 12 %03d %03d", 100+i*7, 200+i*13))}
		tg := int64(9_900_000_001 + i) // placeholder Telegram IDs (link real ones in the panel)
		in.TelegramUserID = &tg
		if b := s.branch[p.branch]; b != nil {
			in.BranchID = &b.ID
		}
		if m, ok := managers[p.branch]; ok && p.role == "staff" && !p.branchAdmin {
			in.ManagerID = &m
		}
		e, err := empSvc.Create(s.ctx, s.cli, in)
		if err != nil {
			return fmt.Errorf("create %s: %w", p.code, err)
		}
		if p.role == "manager" {
			managers[p.branch] = e.ID
		}
		if p.branchAdmin {
			if _, err := brSvc.AssignAdmin(s.ctx, s.cli, s.branch[p.branch].ID, &e.ID); err != nil {
				return fmt.Errorf("assign %s: %w", p.code, err)
			}
		}
		if p.email != "" {
			if _, err := empSvc.CreateUser(s.ctx, s.cli, e.ID, p.email, s.o.Password); err != nil {
				return fmt.Errorf("login %s: %w", p.code, err)
			}
			role := map[string]string{"hr_admin": "HR admin (all branches)", "manager": "Manager"}[p.role]
			if p.branchAdmin {
				role = "Branch admin"
			}
			if b := s.branch[p.branch]; b != nil {
				role += " — " + b.Name
			}
			s.sum.Logins = append(s.sum.Logins, Login{Email: p.email, Name: p.name, Role: role})
		}
		s.sum.Employees++
	}
	// Reload with schedules, roles and branches as assigned.
	var all []models.Employee
	if err := s.d.DB.WithContext(s.ctx).Preload("WorkSchedule").Preload("Branch").Preload("TelegramAccount").
		Where("employee_code LIKE ?", codePrefix+"%").Find(&all).Error; err != nil {
		return err
	}
	for i := range all {
		s.emps[all[i].EmployeeCode] = &all[i]
	}
	return nil
}

// backdate makes the demo staff look established: joined months ago and in the
// system before the generated history (reports never count days before that).
func (s *seeder) backdate() error {
	created := s.today.AddDate(0, 0, -s.o.Days-30)
	for i, p := range people {
		join := s.today.AddDate(-1-i%3, -(i*2)%12, -(i*5)%28)
		if err := s.d.DB.WithContext(s.ctx).Model(&models.Employee{}).Where("employee_code = ?", p.code).
			Updates(map[string]any{"created_at": created, "join_date": join}).Error; err != nil {
			return err
		}
	}
	return nil
}

func (s *seeder) holidays() error {
	for _, h := range holidayDefs {
		res := s.d.DB.WithContext(s.ctx).Clauses(clause.OnConflict{DoNothing: true}).
			Create(&models.Holiday{Date: time.Date(s.today.Year(), h.month, h.day, 0, 0, 0, 0, time.UTC), Name: h.name, CreatedAt: time.Now().UTC()})
		if res.Error != nil {
			return res.Error
		}
		s.sum.Holidays += int(res.RowsAffected)
	}
	var err error
	s.isHol, err = attendance.HolidaySet(s.ctx, s.d.DB, s.today.AddDate(-1, 0, 0), s.today.AddDate(1, 0, 0))
	return err
}

// ---------- leave ----------

// workday returns the n-th working day (Mon–Fri, not a holiday) from today; n<0 goes back.
func (s *seeder) workday(n int) time.Time {
	d, step := s.today, 1
	if n < 0 {
		step, n = -1, -n
	}
	for n > 0 {
		d = d.AddDate(0, 0, step)
		if wd := d.Weekday(); wd != time.Saturday && wd != time.Sunday && !s.isHol(d) {
			n--
		}
	}
	return d
}

func (s *seeder) leave() error {
	var types []models.LeaveType
	if err := s.d.DB.WithContext(s.ctx).Find(&types).Error; err != nil {
		return err
	}
	typeID := map[string]uint64{}
	for _, t := range types {
		typeID[t.Code] = t.ID
	}
	plan := []struct {
		code, typ        string
		start, length    int // first working day relative to today, number of working days
		status, reviewer string
		reason, note     string
	}{
		{"D-PP04", "ANNUAL", -12, 3, models.LeaveApproved, "D-PP01", "Family trip to Kampot", ""},
		{"D-SR03", "SICK", -6, 1, models.LeaveApproved, "D-SR01", "Fever, doctor's note attached", "Get well soon"},
		{"D-PP06", "ANNUAL", -15, 1, models.LeaveRejected, "D-PP01", "Personal errands", "Month-end closing — please choose another day."},
		{"D-BB02", "ANNUAL", 10, 2, models.LeaveApproved, "D-BB01", "Brother's wedding", ""},
		{"D-PP05", "PERSONAL", 4, 2, models.LeavePending, "", "Moving to a new apartment", ""},
		{"D-SR04", "ANNUAL", 7, 1, models.LeavePending, "", "Visiting family in Kampong Cham", ""},
		{"D-BB03", "EMERGENCY", 2, 1, models.LeavePending, "", "Child's school appointment", ""},
		{"D-PP07", "PERSONAL", 15, 1, models.LeaveCancelled, "", "Bank appointment", ""},
	}
	return s.d.DB.WithContext(s.ctx).Transaction(func(tx *gorm.DB) error {
		for _, p := range plan {
			emp := s.emps[p.code]
			start := s.workday(p.start)
			end := start
			for i := 1; i < p.length; i++ {
				end = s.nextWorkday(end)
			}
			requested := start.AddDate(0, 0, -7).Add(3 * time.Hour)
			if requested.After(s.o.Now) {
				requested = s.o.Now.Add(-26 * time.Hour)
			}
			lr := models.LeaveRequest{EmployeeID: emp.ID, LeaveTypeID: typeID[p.typ], StartDate: start, EndDate: end,
				Days: attendance.WorkingDays(emp, start, end, s.isHol), Reason: p.reason, Status: p.status, ReviewNote: p.note,
				CreatedAt: requested.UTC(), UpdatedAt: requested.UTC()}
			if r := s.emps[p.reviewer]; r != nil {
				at := requested.Add(20 * time.Hour).UTC()
				lr.ReviewedBy, lr.ReviewedAt = &r.ID, &at
			}
			if err := tx.Omit(clause.Associations).Create(&lr).Error; err != nil {
				return err
			}
			if p.status == models.LeaveApproved {
				name := map[string]string{"ANNUAL": "Annual Leave", "SICK": "Sick Leave"}[p.typ]
				if err := attendance.MarkLeaveDays(s.ctx, tx, emp, start, end, s.isHol, fmt.Sprintf("Leave #%d (%s)", lr.ID, name)); err != nil {
					return err
				}
			}
			s.sum.LeaveRequests++
		}
		return nil
	})
}

func (s *seeder) nextWorkday(d time.Time) time.Time {
	for {
		d = d.AddDate(0, 0, 1)
		if wd := d.Weekday(); wd != time.Saturday && wd != time.Sunday && !s.isHol(d) {
			return d
		}
	}
}

// ---------- attendance ----------

func (s *seeder) reviewerOf(code string) *models.Employee {
	e := s.emps[code]
	switch {
	case e.RoleID == models.RoleHRAdmin || e.RoleID == models.RoleBranchAdmin:
		return s.owner
	case e.ManagerID != nil:
		for _, m := range s.emps {
			if m.ID == *e.ManagerID {
				return m
			}
		}
	}
	if e.Branch != nil && e.Branch.AdminEmployeeID != nil {
		for _, m := range s.emps {
			if m.ID == *e.Branch.AdminEmployeeID {
				return m
			}
		}
	}
	return s.owner
}

func (s *seeder) attendance() error {
	from := s.today.AddDate(0, 0, -s.o.Days)
	ids := make([]uint64, 0, len(s.emps))
	for _, e := range s.emps {
		ids = append(ids, e.ID)
	}
	var existing []models.Attendance // approved leave days
	if err := s.d.DB.WithContext(s.ctx).Where("employee_id IN ? AND work_date >= ?", ids, from).Find(&existing).Error; err != nil {
		return err
	}
	have := map[string]bool{}
	for _, a := range existing {
		have[fmt.Sprintf("%d|%s", a.EmployeeID, a.WorkDate.Format("2006-01-02"))] = true
	}
	nowLocal := s.o.Now.In(s.loc)
	rejectedDone := false
	return s.d.DB.WithContext(s.ctx).Transaction(func(tx *gorm.DB) error {
		for d := from; !d.After(s.today); d = d.AddDate(0, 0, 1) {
			for i, p := range people {
				e := s.emps[p.code]
				sched := attendance.ScheduleFrom(e.WorkSchedule)
				if !sched.IsWorkDay(d) || s.isHol(d) || have[fmt.Sprintf("%d|%s", e.ID, d.Format("2006-01-02"))] {
					continue
				}
				isToday, isYesterday := d.Equal(s.today), d.Equal(s.workday(-1))
				r := s.rng.Float64()
				if !isToday && r < 0.04 { // absent (what the day-close job would record)
					a := models.Attendance{EmployeeID: e.ID, WorkDate: d, Status: models.StatusAbsent, ConfirmationStatus: models.ConfConfirmed,
						Source: models.SourceSystem, Note: "No check-in recorded"}
					if err := tx.Omit(clause.Associations).Create(&a).Error; err != nil {
						return err
					}
					s.sum.AttendanceDays++
					continue
				}
				in := s.at(d, sched.Start, -25+s.rng.IntN(35)) // 08:35–09:09
				if r < 0.16 {
					in = s.at(d, sched.Start, 12+s.rng.IntN(45)) // late: 09:12–09:56
				}
				if isToday && (in.After(nowLocal) || s.rng.Float64() < 0.12) {
					continue // not checked in (yet)
				}
				var out *time.Time
				switch o := s.rng.Float64(); {
				case isToday && nowLocal.Before(s.at(d, sched.End, 0)):
				case o < 0.03 && !isToday: // forgot to check out
				case o < 0.08:
					t := s.at(d, sched.End, -15-s.rng.IntN(55)) // early leave
					out = &t
				default:
					t := s.at(d, sched.End, s.rng.IntN(50))
					out = &t
				}
				if out != nil && out.After(nowLocal) {
					out = nil
				}
				pendingIn := isToday && i%4 != 0
				pendingOut := isToday || (isYesterday && i%3 == 0)
				rejectOut := isYesterday && !rejectedDone && p.branch == "SR" && p.role == "staff" && !p.branchAdmin && out != nil
				if rejectOut {
					rejectedDone = true
				}
				if err := s.record(tx, e, d, sched, in, out, pendingIn, pendingOut, rejectOut); err != nil {
					return err
				}
				s.sum.AttendanceDays++
			}
		}
		return nil
	})
}

func (s *seeder) at(d time.Time, hhmm string, minutes int) time.Time {
	t, _ := timeutil.At(d, hhmm, s.loc)
	return t.Add(time.Duration(minutes) * time.Minute)
}

// record writes one check-in (and maybe check-out) with photos, GPS points and reviews.
func (s *seeder) record(tx *gorm.DB, e *models.Employee, d time.Time, sched attendance.Schedule, in time.Time, out *time.Time,
	pendingIn, pendingOut, rejectOut bool) error {
	inUTC := in.UTC()
	a := models.Attendance{EmployeeID: e.ID, WorkDate: d, CheckInAt: &inUTC, LateMinutes: sched.LateMinutes(d, in, s.loc), Source: models.SourceTelegram}
	if out != nil {
		outUTC := out.UTC()
		a.CheckOutAt = &outUTC
		a.WorkingMinutes = attendance.WorkingMinutes(in, *out)
		a.EarlyLeaveMinutes = sched.EarlyLeaveMinutes(d, *out, s.loc)
	}
	a.Status = attendance.DeriveStatus(a.LateMinutes, a.EarlyLeaveMinutes)
	reviewer := s.reviewerOf(e.EmployeeCode)

	confs := []models.AttendanceConfirmation{s.conf(models.TypeCheckIn, in, pendingIn, false, reviewer)}
	if out != nil {
		confs = append(confs, s.conf(models.TypeCheckOut, *out, pendingOut, rejectOut, reviewer))
	}
	a.ConfirmationStatus = attendance.DeriveConfirmation(confs)
	if a.ConfirmationStatus == models.ConfConfirmed {
		last := confs[len(confs)-1].ReviewedAt
		a.ConfirmedBy, a.ConfirmedAt = &reviewer.ID, last
	}
	a.CreatedAt, a.UpdatedAt = inUTC, inUTC
	if err := tx.Omit(clause.Associations).Create(&a).Error; err != nil {
		return err
	}
	for i := range confs {
		confs[i].AttendanceID = a.ID
	}
	if err := tx.Create(&confs).Error; err != nil {
		return err
	}
	shots := []struct {
		typ string
		at  time.Time
	}{{models.TypeCheckIn, in}}
	if out != nil {
		shots = append(shots, struct {
			typ string
			at  time.Time
		}{models.TypeCheckOut, *out})
	}
	for _, sh := range shots {
		if err := s.photoAndLocation(tx, e, a.ID, d, sh.typ, sh.at); err != nil {
			return err
		}
	}
	return nil
}

func (s *seeder) conf(typ string, at time.Time, pending, rejected bool, reviewer *models.Employee) models.AttendanceConfirmation {
	c := models.AttendanceConfirmation{Type: typ, Status: models.ConfConfirmed, CreatedAt: at.UTC(), UpdatedAt: at.UTC()}
	switch {
	case rejected:
		c.Status, c.Reason = models.ConfRejected, "Not at the workplace."
	case pending:
		c.Status = models.ConfPending
		return c
	}
	reviewed := at.Add(time.Duration(40+s.rng.IntN(150)) * time.Minute).UTC()
	if reviewed.After(s.o.Now) {
		reviewed = s.o.Now.UTC()
	}
	c.ReviewedBy, c.ReviewedAt, c.UpdatedAt = &reviewer.ID, &reviewed, reviewed
	return c
}

func (s *seeder) photoAndLocation(tx *gorm.DB, e *models.Employee, attID uint64, d time.Time, typ string, at time.Time) error {
	s.photoNo++
	data, err := demoPhoto(s.photoNo, e.ID)
	if err != nil {
		return err
	}
	kind := map[string]string{models.TypeCheckIn: "in", models.TypeCheckOut: "out"}[typ]
	key := fmt.Sprintf("attendance/%s/%s/%s-%s-demo%d.jpg", d.Format("2006/01/02"), e.EmployeeCode, kind, at.In(s.loc).Format("150405"), s.photoNo)
	if err := s.d.Store.Put(s.ctx, key, data, "image/jpeg"); err != nil {
		return fmt.Errorf("photo storage: %w (is the storage service running?)", err)
	}
	sum := sha256.Sum256(data)
	var tg int64
	if acc := e.TelegramAccount; acc != nil {
		tg = acc.TelegramUserID
	}
	photo := models.AttendancePhoto{AttendanceID: attID, Type: typ, ContentSHA256: hex.EncodeToString(sum[:]), TelegramUserID: tg,
		StorageKey: key, StorageURL: s.d.Store.URI(key), ContentType: "image/jpeg", SizeBytes: int64(len(data)), CreatedAt: at.UTC()}
	if err := tx.Create(&photo).Error; err != nil {
		return err
	}
	// GPS near the employee's office (the company office for staff without a branch).
	lat, lng, radius := 11.5564, 104.9282, 150.0
	if b := e.Branch; b.HasOffice() {
		lat, lng, radius = *b.OfficeLatitude, *b.OfficeLongitude, *b.AllowedRadiusMeters
	}
	spread := 0.0009 // ~100 m
	if s.rng.Float64() < 0.05 {
		spread = 0.006 // occasionally well outside the office
	}
	pLat, pLng := lat+(s.rng.Float64()*2-1)*spread, lng+(s.rng.Float64()*2-1)*spread
	dist := attendance.DistanceMeters(pLat, pLng, lat, lng)
	inside := dist <= radius
	acc := 6 + s.rng.Float64()*20
	loc := models.AttendanceLocation{AttendanceID: attID, Type: typ, Latitude: pLat, Longitude: pLng, Accuracy: &acc,
		DistanceMeters: &dist, InsideOffice: &inside, CreatedAt: at.UTC()}
	return tx.Create(&loc).Error
}

func (s *seeder) audit() error {
	return audit.Write(s.ctx, s.d.DB, s.cli, audit.Entry{Action: "demo.seed", EntityType: "demo",
		Details: map[string]any{"employees": s.sum.Employees, "attendance_days": s.sum.AttendanceDays, "leave_requests": s.sum.LeaveRequests}})
}

// demoPhoto draws a simple "selfie" placeholder (unique per call).
func demoPhoto(n, person uint64) ([]byte, error) {
	palette := []color.RGBA{{52, 101, 164, 255}, {78, 154, 6, 255}, {196, 160, 0, 255}, {117, 80, 123, 255}, {204, 0, 0, 255}, {6, 152, 154, 255}}
	bg := palette[person%uint64(len(palette))]
	img := image.NewRGBA(image.Rect(0, 0, 240, 320))
	skin := color.RGBA{238, 200, 160, 255}
	shirt := color.RGBA{240, 240, 240, 255}
	for y := 0; y < 320; y++ {
		for x := 0; x < 240; x++ {
			c := bg
			shade := uint8((y * 40) / 320)
			c.R, c.G, c.B = sat(c.R, shade), sat(c.G, shade), sat(c.B, shade)
			dx, dy := x-120, y-130
			switch {
			case dx*dx+dy*dy < 55*55: // head
				c = skin
			case y > 200 && (x-120)*(x-120)*4+(y-320)*(y-320) < 120*120*4: // shoulders
				c = shirt
			}
			img.Set(x, y, c)
		}
	}
	// a few seeded specks so every photo is a distinct image
	r := rand.New(rand.NewPCG(n, person))
	for i := 0; i < 60; i++ {
		img.Set(r.IntN(240), r.IntN(320), color.RGBA{uint8(r.IntN(256)), uint8(r.IntN(256)), uint8(r.IntN(256)), 255})
	}
	var buf bytes.Buffer
	if err := jpeg.Encode(&buf, img, &jpeg.Options{Quality: 72}); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

func sat(v, add uint8) uint8 {
	if int(v)+int(add) > 255 {
		return 255
	}
	return v + add
}

func ptr(s string) *string { return &s }
