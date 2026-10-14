// Package httpapi exposes the REST API for the HR admin panel (/api/v1, JWT) and
// the Telegram Mini App (/api/v1/app, signed Telegram initData).
package httpapi

import (
	"context"
	"net"
	"net/http"
	"time"

	"attendance/backend/internal/attendance"
	"attendance/backend/internal/auth"
	"attendance/backend/internal/branch"
	"attendance/backend/internal/config"
	"attendance/backend/internal/employee"
	"attendance/backend/internal/leave"
	"attendance/backend/internal/report"
	"attendance/backend/internal/schedule"
	"attendance/backend/internal/settings"
	"attendance/backend/internal/storage"
	"attendance/backend/internal/telegram"

	"github.com/gin-gonic/gin"
	"github.com/redis/go-redis/v9"
	"gorm.io/gorm"
)

// Deps are the services the HTTP layer delegates to.
type Deps struct {
	DB         *gorm.DB
	Redis      *redis.Client
	Store      storage.Store
	Auth       *auth.Service
	Settings   *settings.Service
	Employees  *employee.Service
	Branches   *branch.Service
	Attendance *attendance.Service
	Leave      *leave.Service
	Reports    *report.Service
	Schedules  *schedule.Service
	Telegram   *telegram.Client // verifies bot tokens and updates the bot's menu button
}

type Server struct {
	cfg        *config.Config
	db         *gorm.DB
	rdb        *redis.Client
	store      storage.Store
	auth       *auth.Service
	settings   *settings.Service
	employees  *employee.Service
	branches   *branch.Service
	attendance *attendance.Service
	leave      *leave.Service
	reports    *report.Service
	schedules  *schedule.Service
	tg         *telegram.Client
}

func New(cfg *config.Config, d Deps) *Server {
	return &Server{cfg: cfg, db: d.DB, rdb: d.Redis, store: d.Store, auth: d.Auth, settings: d.Settings, employees: d.Employees,
		branches: d.Branches, attendance: d.Attendance, leave: d.Leave, reports: d.Reports, schedules: d.Schedules, tg: d.Telegram}
}

func (s *Server) Router() *gin.Engine {
	if s.cfg.IsProduction() {
		gin.SetMode(gin.ReleaseMode)
	}
	r := gin.New()
	// Trust X-Forwarded-For only from the proxy in front (TRUSTED_PROXIES; config validates it).
	_ = r.SetTrustedProxies(s.cfg.TrustedProxies)
	if h := s.cfg.ClientIPHeader; h != "" {
		r.TrustedPlatform = h // the platform's edge sets it to the visitor's IP
		r.Use(clientIPHeader(h))
	}
	uploadLimit := int64(attendance.MaxUploadBytes + 64<<10) // photo + multipart overhead
	r.Use(gin.Recovery(), requestID(), accessLog(), securityHeaders(), bodyLimit(1<<20, map[string]int64{
		"/api/v1/app/attendance/check-in":  uploadLimit,
		"/api/v1/app/attendance/check-out": uploadLimit,
	}), cors(s.cfg.CORSOrigins))

	r.GET("/health", s.health)

	byIP := func(c *gin.Context) string { return c.ClientIP() }
	byActor := func(c *gin.Context) string {
		if a := actorFrom(c); a != nil {
			return a.Employee.EmployeeCode
		}
		return c.ClientIP()
	}

	v1 := r.Group("/api/v1")
	v1.POST("/auth/login", rateLimit(s.rdb, "login", 10, time.Minute, byIP), s.login)

	api := v1.Group("", s.jwtAuth(), rateLimit(s.rdb, "api", 600, time.Minute, byActor))
	api.GET("/auth/me", s.me)
	api.POST("/auth/logout", s.logout)

	api.GET("/employees", s.listEmployees)
	api.POST("/employees", require(auth.PermStaffManage), s.createEmployee)
	api.GET("/employees/:id", s.getEmployee)
	api.PUT("/employees/:id", require(auth.PermStaffManage), s.updateEmployee)
	api.DELETE("/employees/:id", require(auth.PermStaffManage), s.deactivateEmployee)
	api.POST("/employees/:id/user", require(auth.PermAssignAdminRoles), s.createUser)
	api.GET("/branches", s.listBranches)
	api.POST("/branches", require(auth.PermBranchesManage), s.createBranch)
	api.GET("/branches/:id", s.getBranch)
	api.PUT("/branches/:id", s.updateBranch) // owner, or the branch's own admin (ops fields only)
	api.PUT("/branches/:id/admin", require(auth.PermBranchesManage), s.assignBranchAdmin)
	api.GET("/departments", s.listDepartments)
	api.GET("/work-schedules", s.listShifts)
	api.POST("/work-schedules", require(auth.PermSchedulesManage), s.createShift)
	api.PUT("/work-schedules/:id", require(auth.PermSchedulesManage), s.updateShift)
	api.DELETE("/work-schedules/:id", require(auth.PermSchedulesManage), s.deleteShift)
	api.GET("/roster", s.getRoster)
	api.PUT("/roster", require(auth.PermStaffManage), s.setRoster)
	api.POST("/roster/copy", require(auth.PermStaffManage), s.copyRoster)
	api.GET("/roster/export", require(auth.PermReportExport), rateLimit(s.rdb, "export", 20, time.Minute, byActor), s.exportRoster)
	api.GET("/schedule-overrides", s.listOverrides)
	api.POST("/schedule-overrides", require(auth.PermStaffManage), s.createOverride)
	api.PUT("/schedule-overrides/:id", require(auth.PermStaffManage), s.updateOverride)
	api.DELETE("/schedule-overrides/:id", require(auth.PermStaffManage), s.deleteOverride)

	api.GET("/attendance", s.listAttendance)
	api.POST("/attendance", require(auth.PermAttendanceEdit), s.manualAttendance)
	api.GET("/attendance/pending", requireReviewer(), s.pendingAttendance)
	api.GET("/attendance/photos/:id", s.attendancePhoto)
	api.GET("/attendance/:id", s.getAttendance)
	api.PUT("/attendance/:id", require(auth.PermAttendanceEdit), s.editAttendance)
	api.POST("/attendance/confirmations/:id/confirm", requireReviewer(), s.reviewAttendance(true))
	api.POST("/attendance/confirmations/:id/reject", requireReviewer(), s.reviewAttendance(false))

	api.GET("/leave", s.listLeave)
	api.GET("/leave/types", s.leaveTypes)
	api.GET("/leave/pending", requireReviewer(), s.pendingLeave)
	api.GET("/leave/:id", s.getLeave)
	api.POST("/leave/:id/approve", requireReviewer(), s.reviewLeave(true))
	api.POST("/leave/:id/reject", requireReviewer(), s.reviewLeave(false))

	api.GET("/reports/daily", s.dailyReport)
	api.GET("/reports/summary", s.summaryReport)
	api.GET("/reports/export", require(auth.PermReportExport), rateLimit(s.rdb, "export", 20, time.Minute, byActor), s.exportReport)

	api.GET("/settings", require(auth.PermSettingsView), s.getSettings)
	api.PUT("/settings", require(auth.PermSettingsEdit), s.updateSettings)
	api.GET("/holidays", s.listHolidays)
	api.POST("/holidays", require(auth.PermSettingsEdit), s.createHoliday)
	api.DELETE("/holidays/:id", require(auth.PermSettingsEdit), s.deleteHoliday)
	api.GET("/audit-logs", require(auth.PermAuditView), s.auditLogs)

	app := v1.Group("/app", s.webAppAuth(), rateLimit(s.rdb, "app", 300, time.Minute, byActor))
	s.mountApp(app, byActor)

	if s.cfg.WebDir != "" {
		mountWeb(r, s.cfg.WebDir)
	}
	return r
}

func (s *Server) health(c *gin.Context) {
	ctx, cancel := context.WithTimeout(c.Request.Context(), 3*time.Second)
	defer cancel()
	res := gin.H{"status": "ok", "database": "connected", "redis": "connected", "storage": "connected"}
	code := http.StatusOK
	if sqlDB, err := s.db.DB(); err != nil || sqlDB.PingContext(ctx) != nil {
		res["database"], res["status"], code = "disconnected", "degraded", http.StatusServiceUnavailable
	}
	if s.rdb.Ping(ctx).Err() != nil {
		res["redis"], res["status"], code = "disconnected", "degraded", http.StatusServiceUnavailable
	}
	if s.store.Ping(ctx) != nil {
		res["storage"], res["status"] = "disconnected", "degraded"
	}
	c.JSON(code, res)
}

// HTTPServer wraps the router with sane timeouts.
func (s *Server) HTTPServer(addr string) *http.Server {
	return &http.Server{
		Addr:              addr,
		Handler:           s.Router(),
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       30 * time.Second,
		WriteTimeout:      120 * time.Second,
		IdleTimeout:       120 * time.Second,
		BaseContext:       func(net.Listener) context.Context { return context.Background() },
	}
}
