package httpapi

import (
	"errors"
	"io"
	"math"
	"net/http"
	"strconv"
	"time"

	"attendance/backend/internal/apperr"
	"attendance/backend/internal/attendance"
	"attendance/backend/internal/auth"
	"attendance/backend/internal/leave"
	"attendance/backend/internal/models"
	"attendance/backend/internal/report"
	"attendance/backend/internal/timeutil"

	"github.com/gin-gonic/gin"
)

// Telegram Mini App API (/api/v1/app, authenticated by webAppAuth). Employees get
// their own day, history and leave; reviewers reuse the admin panel's handlers, so
// every permission and scope check is the same one the panel uses.
func (s *Server) mountApp(g *gin.RouterGroup, byActor func(*gin.Context) string) {
	g.GET("/me", s.appMe)

	g.GET("/attendance/today", s.appToday)
	g.GET("/attendance/history", s.appHistory)
	submitLimit := rateLimit(s.rdb, "app-submit", 20, time.Minute, byActor)
	g.POST("/attendance/check-in", submitLimit, s.appSubmit(models.TypeCheckIn))
	g.POST("/attendance/check-out", submitLimit, s.appSubmit(models.TypeCheckOut))
	g.GET("/attendance/photos/:id", s.attendancePhoto)
	g.GET("/attendance/pending", requireReviewer(), s.pendingAttendance)
	g.GET("/attendance/:id", s.getAttendance)
	g.POST("/attendance/confirmations/:id/confirm", requireReviewer(), s.reviewAttendance(true))
	g.POST("/attendance/confirmations/:id/reject", requireReviewer(), s.reviewAttendance(false))

	g.GET("/leave/types", s.leaveTypes)
	g.GET("/leave/mine", s.appMyLeave)
	g.GET("/leave/balances", s.myLeaveBalances)
	g.GET("/leave/pending", requireReviewer(), s.pendingLeave)
	g.POST("/leave/preview", s.appLeavePreview)
	g.POST("/leave", s.appLeaveSubmit)
	g.GET("/leave/:id", s.getLeave)
	g.POST("/leave/:id/cancel", s.appLeaveCancel)
	g.POST("/leave/:id/approve", requireReviewer(), s.reviewLeave(true))
	g.POST("/leave/:id/reject", requireReviewer(), s.reviewLeave(false))

	g.GET("/reports/daily", requireReviewer(), s.dailyReport)
}

type identifyResp struct {
	EmployeeID   uint64   `json:"employee_id"`
	EmployeeCode string   `json:"employee_code"`
	FullName     string   `json:"full_name"`
	IsSystemUser bool     `json:"is_system_user"` // an admin account: no check-in or leave, the Team tab only
	Role         string   `json:"role"`
	IsReviewer   bool     `json:"is_reviewer"`
	IsAdmin      bool     `json:"is_admin"`
	CanExport    bool     `json:"can_export"`
	CanManage    bool     `json:"can_manage_staff"`
	BranchID     *uint64  `json:"branch_id"`
	BranchName   string   `json:"branch_name"`
	Permissions  []string `json:"permissions"`
}

func identify(a *auth.Actor) identifyResp {
	perms := []string{}
	for _, p := range auth.PermissionsFor(a.Employee.RoleID) {
		if a.Has(p) {
			perms = append(perms, string(p))
		}
	}
	out := identifyResp{EmployeeID: a.ID(), EmployeeCode: a.Employee.EmployeeCode, FullName: a.Employee.FullName, IsSystemUser: a.Employee.IsSystemUser(),
		Role: auth.RoleCode(a.Employee.RoleID), IsReviewer: a.IsReviewer(), IsAdmin: a.IsAdmin(),
		CanExport: a.Has(auth.PermReportExport), CanManage: a.Has(auth.PermStaffManage), BranchID: a.Employee.BranchID, Permissions: perms}
	if a.Employee.Branch != nil {
		out.BranchName = a.Employee.Branch.Name
	}
	return out
}

func (s *Server) appMe(c *gin.Context) {
	a := actorFrom(c)
	if u, ok := c.Get(webAppUserKey); ok {
		wu := u.(auth.WebAppUser)
		s.auth.TouchTelegramProfile(c.Request.Context(), wu.ID, wu.Username, wu.FirstName, wu.LastName)
	}
	st, err := s.settings.Get(c.Request.Context())
	if err != nil {
		fail(c, err)
		return
	}
	e := a.Employee
	plan, err := attendance.LoadPlanner(c.Request.Context(), s.db, nil, time.Time{}, time.Time{})
	if err != nil {
		fail(c, err)
		return
	}
	ws := plan.Usual(&e) // the usual shift; the day-to-day plan is on the home screen
	set := attendance.ParseWorkDays(ws.WorkDays)
	workDays := []int{}
	for d := 1; d <= 7; d++ {
		if set[d] {
			workDays = append(workDays, d)
		}
	}
	profile := gin.H{"employee_code": e.EmployeeCode, "full_name": e.FullName, "position": e.Position, "status": e.Status,
		"department": "", "branch": "", "role_name": "", "schedule": gin.H{"name": ws.Name, "start": ws.StartTime, "end": ws.EndTime,
			"cross_day": ws.WorkType == models.WorkCrossDay, "grace_minutes": ws.GraceMinutes, "work_days": workDays}}
	if e.Department != nil {
		profile["department"] = e.Department.Name
	}
	if e.Role != nil {
		profile["role_name"] = e.Role.Name
	}
	if e.Branch != nil {
		profile["branch"] = e.Branch.Name
	}
	c.JSON(http.StatusOK, gin.H{
		"me":      identify(a),
		"profile": profile,
		"settings": gin.H{"company_name": st.CompanyName, "timezone": st.Timezone, "gps_enabled": st.GPSEnabled,
			"gps_required": st.GPSRequired, "confirmation_required": st.ConfirmationRequired},
	})
}

func (s *Server) appToday(c *gin.Context) {
	out, err := s.attendance.Today(c.Request.Context(), actorFrom(c))
	if err != nil {
		fail(c, err)
		return
	}
	c.JSON(http.StatusOK, out)
}

// appHistory returns the employee's own month (?month=YYYY-MM, default this month) up to today.
func (s *Server) appHistory(c *gin.Context) {
	a := actorFrom(c)
	ctx := c.Request.Context()
	today, _ := s.reports.Today(ctx)
	month := today
	if v := c.Query("month"); v != "" {
		m, err := time.Parse("2006-01", v)
		if err != nil {
			fail(c, apperr.BadRequest("INVALID_MONTH", "Use a month like 2026-10."))
			return
		}
		month = m
	}
	from, to := timeutil.MonthRange(month)
	if to.After(today) {
		to = today
	}
	if from.After(today) {
		fail(c, apperr.BadRequest("FUTURE_MONTH", "That month has not started yet."))
		return
	}
	rep, err := s.reports.Build(ctx, a, report.Filter{EmployeeIDs: []uint64{a.ID()}, From: from, To: to})
	if err != nil {
		fail(c, err)
		return
	}
	var sum *report.Summary
	if len(rep.Summaries) > 0 {
		sum = &rep.Summaries[0]
	}
	c.JSON(http.StatusOK, gin.H{"from": from, "to": to, "rows": rep.Rows, "summary": sum})
}

// appSubmit takes a form (multipart or urlencoded): optional latitude, longitude,
// accuracy, and an optional photo file (the Mini App no longer sends one).
func (s *Server) appSubmit(typ string) gin.HandlerFunc {
	return func(c *gin.Context) {
		data, err := formPhoto(c)
		if err != nil {
			fail(c, err)
			return
		}
		in := attendance.SubmitInput{PhotoData: data}
		if loc, err := formLocation(c); err != nil {
			fail(c, err)
			return
		} else {
			in.Location = loc
		}

		a := actorFrom(c)
		var out *attendance.SubmitResult
		if typ == models.TypeCheckIn {
			out, err = s.attendance.CheckIn(c.Request.Context(), a, in)
		} else {
			out, err = s.attendance.CheckOut(c.Request.Context(), a, in)
		}
		if err != nil {
			fail(c, err)
			return
		}
		c.JSON(http.StatusOK, gin.H{"type": out.Type, "attendance": out.Attendance, "pending": out.Pending,
			"resubmission": out.Resubmission, "location_label": out.LocationLabel})
	}
}

// formPhoto reads the optional "photo" file (nil when none was sent).
func formPhoto(c *gin.Context) ([]byte, error) {
	tooBig := apperr.New(http.StatusRequestEntityTooLarge, "PHOTO_TOO_LARGE", "The photo is too large. Please take it again.")
	fh, err := c.FormFile("photo")
	if err != nil {
		var mbe *http.MaxBytesError
		if errors.As(err, &mbe) {
			return nil, tooBig
		}
		return nil, nil // no photo
	}
	if fh.Size > attendance.MaxUploadBytes {
		return nil, tooBig
	}
	f, err := fh.Open()
	if err != nil {
		return nil, apperr.BadRequest("INVALID_PHOTO", "The photo could not be read.")
	}
	defer f.Close()
	data, err := io.ReadAll(io.LimitReader(f, attendance.MaxUploadBytes+1))
	if err != nil {
		return nil, apperr.BadRequest("INVALID_PHOTO", "The photo could not be read.")
	}
	return data, nil
}

func formLocation(c *gin.Context) (*attendance.LocationInput, error) {
	lat, lng := c.PostForm("latitude"), c.PostForm("longitude")
	if lat == "" && lng == "" {
		return nil, nil
	}
	invalid := apperr.BadRequest("INVALID_LOCATION", "Your location could not be read. Please try again.")
	la, err1 := strconv.ParseFloat(lat, 64)
	lo, err2 := strconv.ParseFloat(lng, 64)
	if err1 != nil || err2 != nil || !finite(la) || !finite(lo) || la < -90 || la > 90 || lo < -180 || lo > 180 {
		return nil, invalid
	}
	in := &attendance.LocationInput{Latitude: la, Longitude: lo}
	if v := c.PostForm("accuracy"); v != "" {
		acc, err := strconv.ParseFloat(v, 64)
		if err != nil || !finite(acc) || acc < 0 {
			return nil, invalid
		}
		in.Accuracy = &acc
	}
	return in, nil
}

func finite(f float64) bool { return !math.IsNaN(f) && !math.IsInf(f, 0) }

func (s *Server) appMyLeave(c *gin.Context) {
	a := actorFrom(c)
	items, err := s.leave.List(c.Request.Context(), a, leave.ListFilter{EmployeeID: a.ID(), Limit: 50})
	if err != nil {
		fail(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"items": items})
}

// appLeavePreview checks a request while the form is being filled in: working
// days, the balance, and the employee's shift on the first date (what a half day
// means for a normal or a cross-day shift). A refusal comes back as "problem"
// next to the plan, so the form can still show the shift.
func (s *Server) appLeavePreview(c *gin.Context) {
	var in struct {
		LeaveTypeID uint64 `json:"leave_type_id" binding:"required"`
		StartDate   string `json:"start_date" binding:"required"`
		EndDate     string `json:"end_date" binding:"required"`
		DayType     string `json:"day_type"`
	}
	if !bind(c, &in) {
		return
	}
	lr, bal, plan, err := s.leave.Preview(c.Request.Context(), actorFrom(c),
		leave.SubmitInput{LeaveTypeID: in.LeaveTypeID, StartDate: in.StartDate, EndDate: in.EndDate, DayType: in.DayType, Reason: "preview"})
	if plan == nil {
		fail(c, err)
		return
	}
	out := gin.H{"plan": plan, "balance": bal, "problem": nil, "days": 0}
	if err != nil {
		if e := apperr.As(err); e.Status >= 500 {
			fail(c, err)
			return
		} else {
			out["problem"] = e
		}
	}
	if lr != nil {
		out["days"], out["day_type"], out["half_from"], out["half_to"] = lr.Days, lr.DayType, lr.HalfFrom, lr.HalfTo
		out["start_date"], out["end_date"], out["leave_type"] = lr.StartDate, lr.EndDate, lr.LeaveType
	}
	c.JSON(http.StatusOK, out)
}

func (s *Server) appLeaveSubmit(c *gin.Context) {
	var in leave.SubmitInput
	if !bind(c, &in) {
		return
	}
	lr, err := s.leave.Submit(c.Request.Context(), actorFrom(c), in)
	if err != nil {
		fail(c, err)
		return
	}
	c.JSON(http.StatusCreated, lr)
}

func (s *Server) appLeaveCancel(c *gin.Context) {
	id, ok := idParam(c, "id")
	if !ok {
		return
	}
	lr, err := s.leave.Cancel(c.Request.Context(), actorFrom(c), id)
	if err != nil {
		fail(c, err)
		return
	}
	c.JSON(http.StatusOK, lr)
}
