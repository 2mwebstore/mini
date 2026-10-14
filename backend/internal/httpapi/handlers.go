package httpapi

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"time"

	"attendance/backend/internal/apperr"
	"attendance/backend/internal/attendance"
	"attendance/backend/internal/audit"
	"attendance/backend/internal/auth"
	"attendance/backend/internal/employee"
	"attendance/backend/internal/leave"
	"attendance/backend/internal/models"
	"attendance/backend/internal/notification"
	"attendance/backend/internal/report"
	"attendance/backend/internal/scheduler"
	"attendance/backend/internal/settings"
	"attendance/backend/internal/timeutil"

	"github.com/gin-gonic/gin"
)

// ---------- auth ----------

func (s *Server) login(c *gin.Context) {
	var in struct {
		Email    string `json:"email" binding:"required,max=150"`
		Password string `json:"password" binding:"required,max=200"`
	}
	if !bind(c, &in) {
		return
	}
	res, err := s.auth.Login(c.Request.Context(), in.Email, in.Password)
	if err != nil {
		fail(c, err)
		return
	}
	actor := &auth.Actor{Employee: res.Employee, Channel: auth.ChannelDashboard, IP: c.ClientIP()}
	_ = audit.Write(c.Request.Context(), s.db, actor, audit.Entry{Action: audit.ActionLogin, EntityType: "employee", EntityID: res.Employee.ID})
	s.setSession(c, res.Token, int(time.Until(res.ExpiresAt).Seconds()))
	c.JSON(http.StatusOK, res)
}

func (s *Server) logout(c *gin.Context) {
	s.setSession(c, "", -1)
	if a := actorFrom(c); a != nil {
		_ = audit.Write(c.Request.Context(), s.db, a, audit.Entry{Action: audit.ActionLogout, EntityType: "employee", EntityID: a.ID()})
	}
	c.Status(http.StatusNoContent)
}

func (s *Server) me(c *gin.Context) {
	a := actorFrom(c)
	st, err := s.settings.Get(c.Request.Context())
	if err != nil {
		fail(c, err)
		return
	}
	// The panel formats times in the company timezone; managers can't read /settings.
	all, branchIDs := a.BranchScope()
	if branchIDs == nil {
		branchIDs = []uint64{}
	}
	c.JSON(http.StatusOK, gin.H{"employee": a.Employee, "role": auth.RoleCode(a.Employee.RoleID), "permissions": auth.PermissionsFor(a.Employee.RoleID),
		"company": gin.H{"name": st.CompanyName, "timezone": st.Timezone},
		// where the user's branch / company powers reach: every branch, or these
		"all_branches": all, "branch_ids": branchIDs})
}

// ---------- employees ----------

func (s *Server) listEmployees(c *gin.Context) {
	var f employee.ListFilter
	_ = c.ShouldBindQuery(&f)
	out, err := s.employees.List(c.Request.Context(), actorFrom(c), f)
	if err != nil {
		fail(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"items": out})
}

func (s *Server) createEmployee(c *gin.Context) {
	var in employee.Input
	if !bind(c, &in) {
		return
	}
	e, err := s.employees.Create(c.Request.Context(), actorFrom(c), in)
	if err != nil {
		fail(c, err)
		return
	}
	c.JSON(http.StatusCreated, e)
}

func (s *Server) getEmployee(c *gin.Context) {
	id, ok := idParam(c, "id")
	if !ok {
		return
	}
	e, err := s.employees.Get(c.Request.Context(), actorFrom(c), id)
	if err != nil {
		fail(c, err)
		return
	}
	c.JSON(http.StatusOK, e)
}

func (s *Server) updateEmployee(c *gin.Context) {
	id, ok := idParam(c, "id")
	if !ok {
		return
	}
	var in employee.Input
	if !bind(c, &in) {
		return
	}
	e, err := s.employees.Update(c.Request.Context(), actorFrom(c), id, in)
	if err != nil {
		fail(c, err)
		return
	}
	c.JSON(http.StatusOK, e)
}

func (s *Server) deactivateEmployee(c *gin.Context) {
	id, ok := idParam(c, "id")
	if !ok {
		return
	}
	if err := s.employees.Deactivate(c.Request.Context(), actorFrom(c), id); err != nil {
		fail(c, err)
		return
	}
	c.Status(http.StatusNoContent)
}

func (s *Server) createUser(c *gin.Context) {
	id, ok := idParam(c, "id")
	if !ok {
		return
	}
	var in struct {
		Email    string `json:"email" binding:"required"`
		Password string `json:"password" binding:"required,max=200"`
	}
	if !bind(c, &in) {
		return
	}
	u, err := s.employees.CreateUser(c.Request.Context(), actorFrom(c), id, in.Email, in.Password)
	if err != nil {
		fail(c, err)
		return
	}
	c.JSON(http.StatusCreated, u)
}

func (s *Server) listDepartments(c *gin.Context) {
	out, err := s.employees.DepartmentList(c.Request.Context())
	if err != nil {
		fail(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"items": out})
}

// ---------- attendance ----------

func (s *Server) listAttendance(c *gin.Context) {
	var f attendance.ListFilter
	_ = c.ShouldBindQuery(&f)
	out, err := s.attendance.List(c.Request.Context(), actorFrom(c), f)
	if err != nil {
		fail(c, err)
		return
	}
	c.JSON(http.StatusOK, out)
}

func (s *Server) getAttendance(c *gin.Context) {
	id, ok := idParam(c, "id")
	if !ok {
		return
	}
	d, err := s.attendance.Detail(c.Request.Context(), actorFrom(c), id)
	if err != nil {
		fail(c, err)
		return
	}
	a := d.Attendance
	// Served under the admin panel API (/api/v1) or the Mini App (/api/v1/app); link photos under the same prefix.
	prefix := strings.TrimSuffix(c.FullPath(), "/attendance/:id")
	photos := make([]gin.H, 0, len(a.Photos))
	for _, p := range a.Photos {
		photos = append(photos, gin.H{"id": p.ID, "type": p.Type, "created_at": p.CreatedAt, "url": fmt.Sprintf("%s/attendance/photos/%d", prefix, p.ID)})
	}
	c.JSON(http.StatusOK, gin.H{"attendance": a, "photos": photos, "schedule": d.Schedule, "holiday": d.Holiday, "leave": d.Leave})
}

// attendancePhoto streams the image through the API so storage stays private.
func (s *Server) attendancePhoto(c *gin.Context) {
	id, ok := idParam(c, "id")
	if !ok {
		return
	}
	p, err := s.attendance.PhotoByID(c.Request.Context(), actorFrom(c), id)
	if err != nil {
		fail(c, err)
		return
	}
	rc, size, ct, err := s.store.Get(c.Request.Context(), p.StorageKey)
	if err != nil {
		fail(c, apperr.New(http.StatusBadGateway, "STORAGE_ERROR", "Photo is unavailable."))
		return
	}
	defer rc.Close()
	c.Header("Cache-Control", "private, max-age=300")
	c.DataFromReader(http.StatusOK, size, ct, io.LimitReader(rc, size), nil)
}

func (s *Server) manualAttendance(c *gin.Context) {
	var in attendance.ManualInput
	if !bind(c, &in) {
		return
	}
	a := actorFrom(c)
	out, err := s.attendance.Manual(c.Request.Context(), a, in)
	if err != nil {
		fail(c, err)
		return
	}
	c.JSON(http.StatusOK, out)
}

func (s *Server) editAttendance(c *gin.Context) {
	id, ok := idParam(c, "id")
	if !ok {
		return
	}
	a := actorFrom(c)
	existing, err := s.attendance.Get(c.Request.Context(), a, id)
	if err != nil {
		fail(c, err)
		return
	}
	var in attendance.ManualInput
	in.EmployeeID, in.Date = existing.EmployeeID, existing.WorkDate.Format("2006-01-02")
	var body struct {
		CheckIn  string `json:"check_in"`
		CheckOut string `json:"check_out"`
		Status   string `json:"status"`
		Reason   string `json:"reason" binding:"required,max=500"`
	}
	if !bind(c, &body) {
		return
	}
	in.CheckIn, in.CheckOut, in.Status, in.Reason = body.CheckIn, body.CheckOut, body.Status, body.Reason
	out, err := s.attendance.Manual(c.Request.Context(), a, in)
	if err != nil {
		fail(c, err)
		return
	}
	c.JSON(http.StatusOK, out)
}

func (s *Server) pendingAttendance(c *gin.Context) {
	out, err := s.attendance.Pending(c.Request.Context(), actorFrom(c), 200)
	if err != nil {
		fail(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"items": out})
}

func (s *Server) reviewAttendance(approve bool) gin.HandlerFunc {
	return func(c *gin.Context) {
		id, ok := idParam(c, "id")
		if !ok {
			return
		}
		var body struct {
			Reason string `json:"reason"`
		}
		_ = c.ShouldBindJSON(&body)
		out, err := s.attendance.Review(c.Request.Context(), actorFrom(c), id, approve, body.Reason)
		if err != nil {
			fail(c, err)
			return
		}
		c.JSON(http.StatusOK, out)
	}
}

// ---------- leave ----------

func (s *Server) listLeave(c *gin.Context) {
	var f leave.ListFilter
	_ = c.ShouldBindQuery(&f)
	out, err := s.leave.List(c.Request.Context(), actorFrom(c), f)
	if err != nil {
		fail(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"items": out})
}

// leaveTypes: active types; ?all=1 also inactive ones, with usage counts (admin panel).
func (s *Server) leaveTypes(c *gin.Context) {
	if c.Query("all") == "1" {
		out, err := s.leave.AllTypes(c.Request.Context())
		if err != nil {
			fail(c, err)
			return
		}
		c.JSON(http.StatusOK, gin.H{"items": out})
		return
	}
	out, err := s.leave.Types(c.Request.Context())
	if err != nil {
		fail(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"items": out})
}

func (s *Server) pendingLeave(c *gin.Context) {
	out, err := s.leave.PendingForReviewer(c.Request.Context(), actorFrom(c))
	if err != nil {
		fail(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"items": out})
}

func (s *Server) getLeave(c *gin.Context) {
	id, ok := idParam(c, "id")
	if !ok {
		return
	}
	out, err := s.leave.Get(c.Request.Context(), actorFrom(c), id)
	if err != nil {
		fail(c, err)
		return
	}
	c.JSON(http.StatusOK, out)
}

func (s *Server) reviewLeave(approve bool) gin.HandlerFunc {
	return func(c *gin.Context) {
		id, ok := idParam(c, "id")
		if !ok {
			return
		}
		var body struct {
			Note string `json:"note"`
		}
		_ = c.ShouldBindJSON(&body)
		out, err := s.leave.Review(c.Request.Context(), actorFrom(c), id, approve, body.Note)
		if err != nil {
			fail(c, err)
			return
		}
		c.JSON(http.StatusOK, out)
	}
}

// ---------- reports ----------

func (s *Server) parseRange(c *gin.Context) (time.Time, time.Time, bool) {
	today, _ := s.reports.Today(c.Request.Context())
	from, to := today, today
	var err error
	if v := c.Query("from"); v != "" {
		if from, err = timeutil.ParseDate(v); err != nil {
			fail(c, apperr.BadRequest("INVALID_DATE", err.Error()))
			return from, to, false
		}
	} else {
		from, _ = timeutil.MonthRange(today)
	}
	if v := c.Query("to"); v != "" {
		if to, err = timeutil.ParseDate(v); err != nil {
			fail(c, apperr.BadRequest("INVALID_DATE", err.Error()))
			return from, to, false
		}
	}
	return from, to, true
}

func (s *Server) filterFromQuery(c *gin.Context) (report.Filter, bool) {
	from, to, ok := s.parseRange(c)
	f := report.Filter{From: from, To: to}
	if v, err := strconv.ParseUint(c.Query("employee_id"), 10, 64); err == nil && v > 0 {
		f.EmployeeIDs = []uint64{v}
	}
	if v, err := strconv.ParseUint(c.Query("department_id"), 10, 64); err == nil {
		f.DepartmentID = v
	}
	if v, err := strconv.ParseUint(c.Query("branch_id"), 10, 64); err == nil {
		f.BranchID = v
	}
	return f, ok
}

func (s *Server) dailyReport(c *gin.Context) {
	date, _ := s.reports.Today(c.Request.Context())
	if v := c.Query("date"); v != "" {
		d, err := timeutil.ParseDate(v)
		if err != nil {
			fail(c, apperr.BadRequest("INVALID_DATE", err.Error()))
			return
		}
		date = d
	}
	f := report.Filter{From: date, To: date}
	if v, err := strconv.ParseUint(c.Query("branch_id"), 10, 64); err == nil {
		f.BranchID = v
	}
	rep, err := s.reports.Build(c.Request.Context(), actorFrom(c), f)
	if err != nil {
		fail(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"date": date, "counts": report.CountDay(rep.Rows), "rows": rep.Rows})
}

func (s *Server) summaryReport(c *gin.Context) {
	f, ok := s.filterFromQuery(c)
	if !ok {
		return
	}
	rep, err := s.reports.Build(c.Request.Context(), actorFrom(c), f)
	if err != nil {
		fail(c, err)
		return
	}
	if status := strings.ToUpper(c.Query("status")); status != "" {
		filtered := rep.Rows[:0]
		for _, r := range rep.Rows {
			if r.Status == status || r.Confirmation == status {
				filtered = append(filtered, r)
			}
		}
		rep.Rows = filtered
	}
	c.JSON(http.StatusOK, rep)
}

func (s *Server) exportReport(c *gin.Context) {
	f, ok := s.filterFromQuery(c)
	if !ok {
		return
	}
	a := actorFrom(c)
	data, filename, ctype, err := s.buildExport(c, a, f, c.DefaultQuery("format", "xlsx"))
	if err != nil {
		fail(c, err)
		return
	}
	c.Header("Content-Disposition", fmt.Sprintf(`attachment; filename="%s"`, filename))
	c.Data(http.StatusOK, ctype, data)
}

func (s *Server) buildExport(c *gin.Context, a *auth.Actor, f report.Filter, format string) ([]byte, string, string, error) {
	rep, err := s.reports.Build(c.Request.Context(), a, f)
	if err != nil {
		return nil, "", "", err
	}
	base := fmt.Sprintf("attendance_%s_%s", f.From.Format("20060102"), f.To.Format("20060102"))
	var data []byte
	var filename, ctype string
	switch strings.ToLower(format) {
	case "csv":
		data, err = report.CSV(rep)
		filename, ctype = base+".csv", "text/csv; charset=utf-8"
	case "xlsx", "excel":
		data, err = report.XLSX(rep)
		filename, ctype = base+".xlsx", "application/vnd.openxmlformats-officedocument.spreadsheetml.sheet"
	default:
		return nil, "", "", apperr.BadRequest("INVALID_FORMAT", "Format must be xlsx or csv.")
	}
	if err != nil {
		return nil, "", "", err
	}
	_ = audit.Write(c.Request.Context(), s.db, a, audit.Entry{Action: audit.ActionReportExport, EntityType: "report",
		Details: map[string]any{"from": f.From.Format("2006-01-02"), "to": f.To.Format("2006-01-02"), "format": format, "rows": len(rep.Rows), "employee_ids": f.EmployeeIDs}})
	return data, filename, ctype, nil
}

// ---------- settings / holidays / audit ----------

func (s *Server) getSettings(c *gin.Context) {
	st, err := s.settings.Get(c.Request.Context())
	if err != nil {
		fail(c, err)
		return
	}
	backup, err := scheduler.ReadBackupStatus(s.cfg.BackupStatusFile)
	if err != nil {
		slog.Warn("backup status unreadable", "err", err)
	}
	c.JSON(http.StatusOK, gin.H{"settings": st, "backup": backup})
}

// updateSettings applies {key: value} changes. A new Telegram bot token is checked
// with Telegram before it is saved (encrypted); it is never echoed or audited.
func (s *Server) updateSettings(c *gin.Context) {
	var in map[string]any
	dec := json.NewDecoder(c.Request.Body)
	dec.UseNumber() // keep chat IDs like -1001234567890 exact
	if err := dec.Decode(&in); err != nil {
		fail(c, apperr.BadRequest("INVALID_INPUT", "Invalid request body."))
		return
	}
	changes := map[string]string{}
	for k, v := range in {
		changes[k] = strings.TrimSpace(fmt.Sprint(v))
	}
	ctx := c.Request.Context()
	var botUsername *string
	if tok, ok := changes["telegram_bot_token"]; ok {
		name := ""
		if tok != "" {
			me, err := s.tg.WithToken(tok).GetMe(ctx)
			if err != nil {
				fail(c, apperr.BadRequest("TELEGRAM_TOKEN_REJECTED", "Telegram rejected this bot token. Copy it again from @BotFather."))
				return
			}
			name = me.Username
		}
		botUsername = &name
	}
	a := actorFrom(c)
	prev, err := s.settings.Update(ctx, a.ID(), changes)
	if err != nil {
		fail(c, err)
		return
	}
	if botUsername != nil {
		if err := s.settings.SetBotUsername(ctx, *botUsername); err != nil {
			slog.Error("save bot username", "err", err)
		}
	}
	after := map[string]string{}
	for k, v := range changes {
		after[k] = v
		if settings.SecretKeys[k] {
			delete(prev, k)
			after[k] = map[bool]string{true: "(cleared)", false: "(changed)"}[v == ""]
		}
	}
	_ = audit.Write(ctx, s.db, a, audit.Entry{Action: audit.ActionSettingsUpdate, EntityType: "settings",
		Details: map[string]any{"before": prev, "after": after}})
	if _, ok := changes["miniapp_url"]; ok || botUsername != nil {
		go notification.SyncBot(context.Background(), s.tg, s.settings) // menu button follows the new URL/bot
	}
	s.getSettings(c)
}

func (s *Server) listHolidays(c *gin.Context) {
	var hs []models.Holiday
	q := s.db.WithContext(c.Request.Context()).Order("date")
	if y, err := strconv.Atoi(c.Query("year")); err == nil {
		q = q.Where("YEAR(date) = ?", y)
	}
	if err := q.Find(&hs).Error; err != nil {
		fail(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"items": hs})
}

func (s *Server) createHoliday(c *gin.Context) {
	var in struct {
		Date string `json:"date" binding:"required"`
		Name string `json:"name" binding:"required,max=150"`
	}
	if !bind(c, &in) {
		return
	}
	d, err := timeutil.ParseDate(in.Date)
	if err != nil {
		fail(c, apperr.BadRequest("INVALID_DATE", err.Error()))
		return
	}
	h := models.Holiday{Date: d, Name: strings.TrimSpace(in.Name), CreatedAt: time.Now().UTC()}
	if err := s.db.WithContext(c.Request.Context()).Create(&h).Error; err != nil {
		fail(c, apperr.Conflict("HOLIDAY_EXISTS", "A holiday already exists on that date."))
		return
	}
	_ = audit.Write(c.Request.Context(), s.db, actorFrom(c), audit.Entry{Action: audit.ActionHolidayCreate, EntityType: "holiday", EntityID: h.ID,
		Details: map[string]any{"date": in.Date, "name": h.Name}})
	c.JSON(http.StatusCreated, h)
}

func (s *Server) deleteHoliday(c *gin.Context) {
	id, ok := idParam(c, "id")
	if !ok {
		return
	}
	var h models.Holiday
	if err := s.db.WithContext(c.Request.Context()).First(&h, id).Error; err != nil {
		fail(c, apperr.NotFound("NOT_FOUND", "Holiday not found."))
		return
	}
	if err := s.db.WithContext(c.Request.Context()).Delete(&h).Error; err != nil {
		fail(c, err)
		return
	}
	_ = audit.Write(c.Request.Context(), s.db, actorFrom(c), audit.Entry{Action: audit.ActionHolidayDelete, EntityType: "holiday", EntityID: h.ID,
		Details: map[string]any{"date": h.Date.Format("2006-01-02"), "name": h.Name}})
	c.Status(http.StatusNoContent)
}

func (s *Server) auditLogs(c *gin.Context) {
	q := s.db.WithContext(c.Request.Context()).Preload("Actor").Preload("Target").Order("id DESC")
	if v := c.Query("action"); v != "" {
		q = q.Where("action = ?", v)
	}
	if v, err := strconv.ParseUint(c.Query("employee_id"), 10, 64); err == nil {
		q = q.Where("target_employee_id = ? OR actor_employee_id = ?", v, v)
	}
	if v := c.Query("before_id"); v != "" {
		q = q.Where("id < ?", v)
	}
	limit, _ := strconv.Atoi(c.DefaultQuery("limit", "100"))
	if limit <= 0 || limit > 500 {
		limit = 100
	}
	var out []models.AuditLog
	if err := q.Limit(limit).Find(&out).Error; err != nil {
		fail(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"items": out})
}
