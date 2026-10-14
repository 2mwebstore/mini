package httpapi

import (
	"fmt"
	"net/http"
	"strconv"
	"time"

	"attendance/backend/internal/apperr"
	"attendance/backend/internal/audit"
	"attendance/backend/internal/schedule"
	"attendance/backend/internal/timeutil"

	"github.com/gin-gonic/gin"
)

// ---------- shifts ----------

func (s *Server) listShifts(c *gin.Context) {
	out, err := s.schedules.Shifts(c.Request.Context())
	if err != nil {
		fail(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"items": out})
}

func (s *Server) createShift(c *gin.Context) {
	var in schedule.ShiftInput
	if !bind(c, &in) {
		return
	}
	ws, err := s.schedules.CreateShift(c.Request.Context(), actorFrom(c), in)
	if err != nil {
		fail(c, err)
		return
	}
	c.JSON(http.StatusCreated, ws)
}

func (s *Server) updateShift(c *gin.Context) {
	id, ok := idParam(c, "id")
	if !ok {
		return
	}
	var in schedule.ShiftInput
	if !bind(c, &in) {
		return
	}
	ws, err := s.schedules.UpdateShift(c.Request.Context(), actorFrom(c), id, in)
	if err != nil {
		fail(c, err)
		return
	}
	c.JSON(http.StatusOK, ws)
}

func (s *Server) deleteShift(c *gin.Context) {
	id, ok := idParam(c, "id")
	if !ok {
		return
	}
	if err := s.schedules.DeleteShift(c.Request.Context(), actorFrom(c), id); err != nil {
		fail(c, err)
		return
	}
	c.Status(http.StatusNoContent)
}

// ---------- roster ----------

func staffFilter(c *gin.Context) schedule.StaffFilter {
	u := func(k string) uint64 {
		v, _ := strconv.ParseUint(c.Query(k), 10, 64)
		return v
	}
	return schedule.StaffFilter{BranchID: u("branch_id"), DepartmentID: u("department_id"), EmployeeID: u("employee_id"), Q: c.Query("q")}
}

// rosterRange: ?from=YYYY-MM-DD&to=… (default: this week, Monday to Sunday).
func (s *Server) rosterRange(c *gin.Context) (time.Time, time.Time, bool) {
	today, _ := s.reports.Today(c.Request.Context())
	from := today.AddDate(0, 0, 1-timeutil.ISOWeekday(today))
	if v := c.Query("from"); v != "" {
		d, err := timeutil.ParseDate(v)
		if err != nil {
			fail(c, apperr.BadRequest("INVALID_DATE", err.Error()))
			return from, from, false
		}
		from = d
	}
	to := from.AddDate(0, 0, 6)
	if v := c.Query("to"); v != "" {
		d, err := timeutil.ParseDate(v)
		if err != nil {
			fail(c, apperr.BadRequest("INVALID_DATE", err.Error()))
			return from, to, false
		}
		to = d
	}
	return from, to, true
}

func (s *Server) getRoster(c *gin.Context) {
	from, to, ok := s.rosterRange(c)
	if !ok {
		return
	}
	r, err := s.schedules.Roster(c.Request.Context(), actorFrom(c), from, to, staffFilter(c))
	if err != nil {
		fail(c, err)
		return
	}
	c.JSON(http.StatusOK, r)
}

func (s *Server) setRoster(c *gin.Context) {
	var in struct {
		Items []schedule.RosterItem `json:"items"`
	}
	if !bind(c, &in) {
		return
	}
	n, err := s.schedules.SetRoster(c.Request.Context(), actorFrom(c), in.Items)
	if err != nil {
		fail(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"updated": n})
}

func (s *Server) copyRoster(c *gin.Context) {
	var in struct {
		From         string `json:"from"`
		To           string `json:"to"`
		BranchID     uint64 `json:"branch_id"`
		DepartmentID uint64 `json:"department_id"`
	}
	if !bind(c, &in) {
		return
	}
	from, err := timeutil.ParseDate(in.From)
	if err != nil {
		fail(c, apperr.BadRequest("INVALID_DATE", err.Error()))
		return
	}
	to, err := timeutil.ParseDate(in.To)
	if err != nil {
		fail(c, apperr.BadRequest("INVALID_DATE", err.Error()))
		return
	}
	n, err := s.schedules.CopyRoster(c.Request.Context(), actorFrom(c), from, to, schedule.StaffFilter{BranchID: in.BranchID, DepartmentID: in.DepartmentID})
	if err != nil {
		fail(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"copied": n})
}

func (s *Server) exportRoster(c *gin.Context) {
	from, to, ok := s.rosterRange(c)
	if !ok {
		return
	}
	a := actorFrom(c)
	r, err := s.schedules.Roster(c.Request.Context(), a, from, to, staffFilter(c))
	if err != nil {
		fail(c, err)
		return
	}
	format := c.DefaultQuery("format", "xlsx")
	data, filename, ctype, err := schedule.Export(r, format)
	if err != nil {
		fail(c, err)
		return
	}
	_ = audit.Write(c.Request.Context(), s.db, a, audit.Entry{Action: audit.ActionReportExport, EntityType: "roster",
		Details: map[string]any{"from": from.Format("2006-01-02"), "to": to.Format("2006-01-02"), "format": format, "employees": len(r.Rows)}})
	c.Header("Content-Disposition", fmt.Sprintf(`attachment; filename="%s"`, filename))
	c.Data(http.StatusOK, ctype, data)
}

// ---------- special / temporary schedules ----------

func (s *Server) listOverrides(c *gin.Context) {
	out, err := s.schedules.Overrides(c.Request.Context(), actorFrom(c), schedule.OverrideFilter{StaffFilter: staffFilter(c), When: c.Query("when")})
	if err != nil {
		fail(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"items": out})
}

func (s *Server) createOverride(c *gin.Context) {
	var in schedule.OverrideInput
	if !bind(c, &in) {
		return
	}
	o, err := s.schedules.CreateOverride(c.Request.Context(), actorFrom(c), in)
	if err != nil {
		fail(c, err)
		return
	}
	c.JSON(http.StatusCreated, o)
}

func (s *Server) updateOverride(c *gin.Context) {
	id, ok := idParam(c, "id")
	if !ok {
		return
	}
	var in schedule.OverrideInput
	if !bind(c, &in) {
		return
	}
	o, err := s.schedules.UpdateOverride(c.Request.Context(), actorFrom(c), id, in)
	if err != nil {
		fail(c, err)
		return
	}
	c.JSON(http.StatusOK, o)
}

func (s *Server) deleteOverride(c *gin.Context) {
	id, ok := idParam(c, "id")
	if !ok {
		return
	}
	if err := s.schedules.DeleteOverride(c.Request.Context(), actorFrom(c), id); err != nil {
		fail(c, err)
		return
	}
	c.Status(http.StatusNoContent)
}
