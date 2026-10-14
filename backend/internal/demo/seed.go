// Package demo fills a development database with realistic sample data so the
// owner can explore the admin panel: three branches with branch admins,
// managers and staff, four weeks of attendance (GPS, reviews — some late,
// absent, pending or rejected), leave requests (full and half days) and public holidays.
//
// People and branches are created through the normal services (validation and
// audit log included); attendance and leave rows are written directly so no
// Telegram notifications go out. Not for production databases.
package demo

import (
	"context"
	"errors"
	"fmt"
	"math/rand/v2"
	"time"

	"attendance/backend/internal/access"
	"attendance/backend/internal/attendance"
	"attendance/backend/internal/audit"
	"attendance/backend/internal/auth"
	"attendance/backend/internal/branch"
	"attendance/backend/internal/employee"
	"attendance/backend/internal/leave"
	"attendance/backend/internal/models"
	"attendance/backend/internal/schedule"
	"attendance/backend/internal/settings"
	"attendance/backend/internal/timeutil"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// ErrAlreadySeeded: this database already has the demo people (codes D-…).
var ErrAlreadySeeded = errors.New("demo data is already in this database")

const codePrefix = "D-"

type Deps struct {
	DB       *gorm.DB
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
	Branches         []string
	Shifts           int // created (besides the default office hours)
	RosterEntries    int
	SpecialSchedules int
	Employees        int
	AttendanceDays   int
	LeaveRequests    int
	Holidays         int
	Logins           []Login
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

// person is a member of staff (an employee who checks in with the Mini App).
type person struct {
	code, name, role, dept, position string
	branch                           string // branch code
}

var people = []person{
	{"D-PP01", "Sophea Chan", "staff", "Operations", "Branch Manager", "MAIN"},
	{"D-PP02", "Dara Sok", "manager", "Sales", "Sales Supervisor", "MAIN"},
	{"D-PP03", "Bopha Keo", "staff", "Sales", "Sales Executive", "MAIN"},
	{"D-PP04", "Vuthy Ly", "staff", "Sales", "Sales Executive", "MAIN"},
	{"D-PP05", "Sreymom Phan", "staff", "Finance", "Accountant", "MAIN"},
	{"D-PP06", "Piseth Long", "staff", "Development", "Software Developer", "MAIN"},
	{"D-PP07", "Kanha Mao", "staff", "Customer Service", "Customer Support", "MAIN"},

	{"D-SR01", "Vanna Kim", "staff", "Operations", "Branch Manager", "SR"},
	{"D-SR02", "Rithy Heng", "manager", "Operations", "Front Office Supervisor", "SR"},
	{"D-SR03", "Sothea Nhem", "staff", "Customer Service", "Receptionist", "SR"},
	{"D-SR04", "Kosal Pen", "staff", "Operations", "Driver", "SR"},
	{"D-SR05", "Nita Ros", "staff", "Sales", "Sales Executive", "SR"},

	{"D-BB01", "Malis Ouk", "staff", "Operations", "Branch Manager", "BB"},
	{"D-BB02", "Narin Seng", "staff", "Sales", "Sales Executive", "BB"},
	{"D-BB03", "Phalla Touch", "staff", "Finance", "Cashier", "BB"},
	{"D-BB04", "Ratha Chhim", "staff", "Operations", "Warehouse Officer", "BB"},
}

// sysUser is an admin panel account (Administration → System users), separate
// from staff. The branch managers above also have one, as each branch's admin:
// one person, two records — they check in as staff and run the panel as admins.
type sysUser struct {
	key, name, role string
	branch          string // branch code for branch-level roles
	email, label    string
}

var sysUsers = []sysUser{
	{"A-HR", "Chenda Lim", "hr_admin", "", "hr@demo.local", "HR admin (all branches)"},
	{"A-MAIN", "Sophea Chan", "branch_admin", "MAIN", "main.admin@demo.local", "Branch admin"},
	{"A-SR", "Vanna Kim", "branch_admin", "SR", "siemreap.admin@demo.local", "Branch admin"},
	{"A-BB", "Malis Ouk", "branch_admin", "BB", "battambang.admin@demo.local", "Branch admin"},
	// A custom role, limited to two branches on Branch assignment.
	{"A-RG", "Sokunthea Ly", "regional_supervisor", "", "regional@demo.local", "Regional supervisor (Siem Reap + Battambang)"},
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
	ctx    context.Context
	d      Deps
	o      Options
	cli    *auth.Actor
	loc    *time.Location
	today  time.Time
	rng    *rand.Rand
	emps   map[string]*models.Employee // staff by code
	admins map[string]*models.Employee // system users by key (A-…)
	branch map[string]*models.Branch   // by code
	owner  *models.Employee
	isHol  func(time.Time) bool
	plan   *attendance.Planner // who works which shift, after roster and special schedules
	sum    *Summary
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
		emps: map[string]*models.Employee{}, admins: map[string]*models.Employee{}, branch: map[string]*models.Branch{}, sum: &Summary{},
	}
	steps := []func() error{s.ensureOwner, s.branches, s.roles, s.systemUsers, s.staff, s.access, s.backdate, s.holidays, s.schedules, s.leave, s.attendance, s.audit}
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
	name, email, role := "Demo Owner", "owner@demo.local", "super_admin"
	u, err := access.NewService(s.d.DB).CreateUser(s.ctx, s.cli, access.UserInput{FullName: &name, Email: &email, Password: &s.o.Password, Role: &role})
	if err != nil {
		return fmt.Errorf("create owner: %w", err)
	}
	var e models.Employee
	if err := s.d.DB.WithContext(s.ctx).First(&e, u.PersonID).Error; err != nil {
		return err
	}
	s.owner = &e
	s.sum.Logins = append(s.sum.Logins, Login{Email: email, Name: name, Role: "Owner (super admin)"})
	return nil
}

// systemUsers adds the admin panel accounts: HR, a branch admin per branch and a
// regional supervisor (a custom role).
func (s *seeder) systemUsers() error {
	svc := access.NewService(s.d.DB)
	for _, a := range sysUsers {
		name, email, role := a.name, a.email, a.role
		in := access.UserInput{FullName: &name, Email: &email, Password: &s.o.Password, Role: &role}
		if b := s.branch[a.branch]; b != nil {
			in.BranchID = &b.ID
		}
		u, err := svc.CreateUser(s.ctx, s.cli, in)
		if err != nil {
			return fmt.Errorf("system user %s: %w", a.email, err)
		}
		var e models.Employee
		if err := s.d.DB.WithContext(s.ctx).First(&e, u.PersonID).Error; err != nil {
			return err
		}
		s.admins[a.key] = &e
		label := a.label
		if b := s.branch[a.branch]; b != nil {
			label += " — " + b.Name
		}
		s.sum.Logins = append(s.sum.Logins, Login{Email: a.email, Name: a.name, Role: label})
	}
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
	managers := map[string]uint64{} // branch code -> manager employee id
	for i, p := range people {
		in := employee.Input{EmployeeCode: p.code, FullName: p.name, Role: p.role, Department: p.dept, Position: ptr(p.position),
			Phone: ptr(fmt.Sprintf("+855 12 %03d %03d", 100+i*7, 200+i*13))}
		tg := int64(9_900_000_001 + i) // placeholder Telegram IDs (link real ones in the panel)
		in.TelegramUserID = &tg
		if b := s.branch[p.branch]; b != nil {
			in.BranchID = &b.ID
		}
		if m, ok := managers[p.branch]; ok && p.role == "staff" && p.position != "Branch Manager" {
			in.ManagerID = &m
		}
		e, err := empSvc.Create(s.ctx, s.cli, in)
		if err != nil {
			return fmt.Errorf("create %s: %w", p.code, err)
		}
		if p.role == "manager" {
			managers[p.branch] = e.ID
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

// ---------- roles, branch access, leave balances ----------

// roles adds a custom role (view and review everyone, edit attendance, export) —
// the A-RG system user has it, limited to two branches by access().
func (s *seeder) roles() error {
	name, desc := "Regional Supervisor", "Oversees the branches assigned to them"
	_, err := access.NewService(s.d.DB).CreateRole(s.ctx, s.cli, access.RoleInput{Name: &name, Description: &desc, Permissions: []auth.Permission{
		auth.PermAttendanceSelf, auth.PermLeaveSelf, auth.PermTeamView, auth.PermTeamReview, auth.PermAllView, auth.PermAllReview,
		auth.PermAttendanceEdit, auth.PermReportExport}})
	if err != nil {
		return fmt.Errorf("role: %w", err)
	}
	return nil
}

// access limits the regional supervisor to Siem Reap and Battambang, and gives
// one person a longer annual leave entitlement with a carry-over.
func (s *seeder) access() error {
	var u models.User
	if err := s.d.DB.WithContext(s.ctx).Where("employee_id = ?", s.admins["A-RG"].ID).First(&u).Error; err != nil {
		return err
	}
	if _, err := access.NewService(s.d.DB).SetBranches(s.ctx, s.cli, u.ID, []uint64{s.branch["SR"].ID, s.branch["BB"].ID}); err != nil {
		return fmt.Errorf("branch access: %w", err)
	}
	var annual models.LeaveType
	if err := s.d.DB.WithContext(s.ctx).Where("code = ?", "ANNUAL").First(&annual).Error; err != nil {
		return err
	}
	return leave.NewService(s.d.DB, s.d.Settings, nil).SetBalance(s.ctx, s.cli, leave.BalanceInput{EmployeeID: s.emps["D-PP06"].ID,
		LeaveTypeID: annual.ID, Year: s.today.Year(), Allocated: 20, Carried: 2, Note: "Long service (5 years) + 2 days from last year"})
}

// ---------- shifts, roster, special schedules ----------

// schedules adds a morning and a night (cross-day) shift, gives two people those
// as usual shifts, puts a few weekend / rotation days on the weekly roster and
// creates special schedules (custom evening hours, a temporary night shift, days off).
func (s *seeder) schedules() error {
	svc := schedule.NewService(s.d.DB, s.d.Settings)
	shift := func(name, start, end, wt string, grace int, days []int, color string) (*models.WorkSchedule, error) {
		ws, err := svc.CreateShift(s.ctx, s.cli, schedule.ShiftInput{Name: &name, StartTime: &start, EndTime: &end, WorkType: &wt,
			GraceMinutes: &grace, WorkDays: days, Color: &color})
		if err != nil {
			return nil, fmt.Errorf("shift %s: %w", name, err)
		}
		s.sum.Shifts++
		return ws, nil
	}
	morning, err := shift("Morning Shift", "07:00", "15:00", models.WorkNormal, 10, []int{1, 2, 3, 4, 5, 6}, "#0891b2")
	if err != nil {
		return err
	}
	night, err := shift("Night Shift", "22:00", "06:00", models.WorkCrossDay, 15, []int{1, 2, 3, 4, 5, 6}, "#4f46e5")
	if err != nil {
		return err
	}
	for code, ws := range map[string]*models.WorkSchedule{"D-BB04": night, "D-SR04": morning} {
		e := s.emps[code]
		if err := s.d.DB.WithContext(s.ctx).Model(&models.Employee{}).Where("id = ?", e.ID).Update("work_schedule_id", ws.ID).Error; err != nil {
			return err
		}
		e.WorkScheduleID, e.WorkSchedule = &ws.ID, ws
	}

	// Weekly roster around this week (Monday-based), past days included so history matches.
	monday := s.today.AddDate(0, 0, 1-timeutil.ISOWeekday(s.today))
	day := func(week, weekday int) string { return monday.AddDate(0, 0, week*7+weekday-1).Format("2006-01-02") }
	on := func(code string, d string, ws *models.WorkSchedule) schedule.RosterItem {
		it := schedule.RosterItem{EmployeeID: s.emps[code].ID, Date: d}
		if ws == nil {
			it.Off = true
		} else {
			it.WorkScheduleID = &ws.ID
		}
		return it
	}
	items := []schedule.RosterItem{
		on("D-SR03", day(-1, 6), morning), on("D-SR03", day(0, 1), morning), on("D-SR03", day(0, 2), morning), on("D-SR03", day(0, 6), morning),
		on("D-SR05", day(0, 5), nil), on("D-SR05", day(0, 6), morning),
		on("D-PP07", day(0, 6), morning), on("D-PP07", day(1, 6), morning),
		on("D-BB03", day(1, 1), night), on("D-BB03", day(1, 2), night),
		on("D-SR03", day(1, 1), morning), on("D-SR03", day(1, 2), morning),
	}
	n, err := svc.SetRoster(s.ctx, s.cli, items)
	if err != nil {
		return fmt.Errorf("roster: %w", err)
	}
	s.sum.RosterEntries = n

	str := func(v string) *string { return &v }
	grace := 10
	special := []schedule.OverrideInput{
		{EmployeeID: s.emps["D-PP06"].ID, StartDate: s.workday(-5).Format("2006-01-02"), EndDate: s.workday(5).Format("2006-01-02"),
			Kind: models.OverrideCustom, StartTime: str("13:00"), EndTime: str("21:00"), WorkType: str(models.WorkNormal), GraceMinutes: &grace,
			WorkDays: []int{1, 2, 3, 4, 5}, Reason: "Release support — evening cover"},
		{EmployeeID: s.emps["D-PP07"].ID, StartDate: day(2, 1), EndDate: day(2, 7), Kind: models.OverrideShift, WorkScheduleID: &night.ID,
			WorkDays: []int{1, 2, 3, 4, 5}, Reason: "Covering the night help desk"},
		{EmployeeID: s.emps["D-PP04"].ID, StartDate: day(3, 1), EndDate: day(3, 3), Kind: models.OverrideOff,
			Reason: "Company training course in Bangkok"},
	}
	for _, in := range special {
		if _, err := svc.CreateOverride(s.ctx, s.cli, in); err != nil {
			return fmt.Errorf("special schedule: %w", err)
		}
		s.sum.SpecialSchedules++
	}

	ids := make([]uint64, 0, len(s.emps))
	for _, e := range s.emps {
		ids = append(ids, e.ID)
	}
	s.plan, err = attendance.LoadPlanner(s.ctx, s.d.DB, ids, s.today.AddDate(0, 0, -s.o.Days-7), s.today.AddDate(0, 0, 60))
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
		{"D-PP04", "ANNUAL", -12, 3, models.LeaveApproved, "A-MAIN", "Family trip to Kampot", ""},
		{"D-SR03", "SICK", -6, 1, models.LeaveApproved, "A-SR", "Fever, doctor's note attached", "Get well soon"},
		{"D-PP06", "ANNUAL", -15, 1, models.LeaveRejected, "A-MAIN", "Personal errands", "Month-end closing — please choose another day."},
		{"D-BB02", "ANNUAL", 10, 2, models.LeaveApproved, "A-BB", "Brother's wedding", ""},
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
			lr := models.LeaveRequest{EmployeeID: emp.ID, LeaveTypeID: typeID[p.typ], StartDate: start, EndDate: end, DayType: models.LeaveFullDay,
				Days: float64(s.plan.WorkingDays(emp, start, end, s.isHol)), Reason: p.reason, Status: p.status, ReviewNote: p.note,
				CreatedAt: requested.UTC(), UpdatedAt: requested.UTC()}
			if r := s.admins[p.reviewer]; r != nil {
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
		// Half days take their hours from that day's shift: a night shift's first
		// half is 22:00-02:00, a day shift's second half the afternoon.
		halves := []struct {
			code, typ, half, status, reviewer, reason string
			start                                     int
		}{
			{"D-BB04", "PERSONAL", models.LeaveFirstHalf, models.LeavePending, "", "Clinic appointment in the evening", 5},
			{"D-SR03", "ANNUAL", models.LeaveSecondHalf, models.LeaveApproved, "A-SR", "Picking up documents at the ministry", 8},
		}
		for _, h := range halves {
			emp, date := s.emps[h.code], s.workday(h.start)
			day := s.plan.Day(emp, date)
			first, second, _, err := day.Halves(date, s.loc)
			if !day.Work || err != nil {
				continue
			}
			hours := first
			if h.half == models.LeaveSecondHalf {
				hours = second
			}
			requested := s.o.Now.Add(-30 * time.Hour).UTC()
			lr := models.LeaveRequest{EmployeeID: emp.ID, LeaveTypeID: typeID[h.typ], StartDate: date, EndDate: date, DayType: h.half,
				HalfFrom: &hours[0], HalfTo: &hours[1], Days: 0.5, Reason: h.reason, Status: h.status, CreatedAt: requested, UpdatedAt: requested}
			if r := s.admins[h.reviewer]; r != nil {
				at := requested.Add(4 * time.Hour)
				lr.ReviewedBy, lr.ReviewedAt = &r.ID, &at
			}
			if err := tx.Omit(clause.Associations).Create(&lr).Error; err != nil {
				return err
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

// reviewerOf: the employee's manager, else their branch admin (a system user), else the owner.
func (s *seeder) reviewerOf(code string) *models.Employee {
	e := s.emps[code]
	if e.ManagerID != nil {
		for _, m := range s.emps {
			if m.ID == *e.ManagerID {
				return m
			}
		}
	}
	if e.Branch != nil && e.Branch.AdminEmployeeID != nil {
		for _, m := range s.admins {
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
				day := s.plan.Day(e, d) // usual shift, roster or special schedule
				if !day.Work || s.isHol(d) || have[fmt.Sprintf("%d|%s", e.ID, d.Format("2006-01-02"))] {
					continue
				}
				start, _ := day.StartAt(d, s.loc)
				end, _ := day.EndAt(d, s.loc) // the next morning for a night shift
				mins := func(t time.Time, m int) time.Time { return t.Add(time.Duration(m) * time.Minute) }
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
				in := mins(start, -25+s.rng.IntN(35)) // 25 min early .. 9 min after the start
				if r < 0.16 {
					in = mins(start, 12+s.rng.IntN(45)) // late: 12–56 min
				}
				if isToday && (in.After(nowLocal) || s.rng.Float64() < 0.12) {
					continue // not checked in (yet)
				}
				var out *time.Time
				switch o := s.rng.Float64(); {
				case isToday && nowLocal.Before(end):
				case o < 0.03 && !isToday: // forgot to check out
				case o < 0.08:
					t := mins(end, -15-s.rng.IntN(55)) // early leave
					out = &t
				default:
					t := mins(end, s.rng.IntN(50))
					out = &t
				}
				if out != nil && out.After(nowLocal) {
					out = nil
				}
				pendingIn := isToday && i%4 != 0
				pendingOut := isToday || (isYesterday && i%3 == 0)
				rejectOut := isYesterday && !rejectedDone && p.branch == "SR" && p.role == "staff" && p.position != "Branch Manager" && out != nil
				if rejectOut {
					rejectedDone = true
				}
				if err := s.record(tx, e, d, day, in, out, pendingIn, pendingOut, rejectOut); err != nil {
					return err
				}
				s.sum.AttendanceDays++
			}
		}
		return nil
	})
}

// record writes one check-in (and maybe check-out) with GPS points and reviews.
func (s *seeder) record(tx *gorm.DB, e *models.Employee, d time.Time, sched attendance.Day, in time.Time, out *time.Time,
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
		c := &confs[i]
		at := in
		if c.Type == models.TypeCheckOut {
			at = *out
		}
		locID, err := s.location(tx, e, a.ID, c.Type, at)
		if err != nil {
			return err
		}
		c.AttendanceID, c.LocationID = a.ID, &locID
	}
	return tx.Create(&confs).Error
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

// location records where a check-in/out was made: near the employee's office.
func (s *seeder) location(tx *gorm.DB, e *models.Employee, attID uint64, typ string, at time.Time) (uint64, error) {
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
	if err := tx.Create(&loc).Error; err != nil {
		return 0, err
	}
	return loc.ID, nil
}

func (s *seeder) audit() error {
	return audit.Write(s.ctx, s.d.DB, s.cli, audit.Entry{Action: "demo.seed", EntityType: "demo",
		Details: map[string]any{"employees": s.sum.Employees, "attendance_days": s.sum.AttendanceDays, "leave_requests": s.sum.LeaveRequests}})
}

func ptr(s string) *string { return &s }
