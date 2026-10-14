package httpapi

import (
	"context"
	"net/http"
	"strconv"

	"attendance/backend/internal/access"
	"attendance/backend/internal/apperr"
	"attendance/backend/internal/auth"
	"attendance/backend/internal/employee"
	"attendance/backend/internal/leave"

	"github.com/gin-gonic/gin"
)

// Administration: roles, system users and branch access; departments and
// positions; leave types and balances.

// ---------- roles ----------

func (s *Server) listRoles(c *gin.Context) {
	out, err := s.access.Roles(c.Request.Context())
	if err != nil {
		fail(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"items": out})
}

func (s *Server) permissionCatalog(c *gin.Context) {
	c.JSON(http.StatusOK, gin.H{"items": auth.Catalog})
}

func roleID(c *gin.Context) (uint8, bool) {
	id, err := strconv.ParseUint(c.Param("id"), 10, 8)
	if err != nil || id == 0 {
		fail(c, apperr.BadRequest("INVALID_ID", "Invalid id."))
		return 0, false
	}
	return uint8(id), true
}

func (s *Server) createRole(c *gin.Context) {
	var in access.RoleInput
	if !bind(c, &in) {
		return
	}
	r, err := s.access.CreateRole(c.Request.Context(), actorFrom(c), in)
	if err != nil {
		fail(c, err)
		return
	}
	c.JSON(http.StatusCreated, r)
}

func (s *Server) updateRole(c *gin.Context) {
	id, ok := roleID(c)
	if !ok {
		return
	}
	var in access.RoleInput
	if !bind(c, &in) {
		return
	}
	r, err := s.access.UpdateRole(c.Request.Context(), actorFrom(c), id, in)
	if err != nil {
		fail(c, err)
		return
	}
	c.JSON(http.StatusOK, r)
}

func (s *Server) deleteRole(c *gin.Context) {
	id, ok := roleID(c)
	if !ok {
		return
	}
	if err := s.access.DeleteRole(c.Request.Context(), actorFrom(c), id); err != nil {
		fail(c, err)
		return
	}
	c.Status(http.StatusNoContent)
}

// ---------- system users ----------

func (s *Server) listSystemUsers(c *gin.Context) {
	out, err := s.access.Users(c.Request.Context(), actorFrom(c))
	if err != nil {
		fail(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"items": out})
}

func (s *Server) createSystemUser(c *gin.Context) {
	var in access.UserInput
	if !bind(c, &in) {
		return
	}
	u, err := s.access.CreateUser(c.Request.Context(), actorFrom(c), in)
	if err != nil {
		fail(c, err)
		return
	}
	c.JSON(http.StatusCreated, u)
}

func (s *Server) updateSystemUser(c *gin.Context) {
	id, ok := idParam(c, "id")
	if !ok {
		return
	}
	var in access.UserInput
	if !bind(c, &in) {
		return
	}
	u, err := s.access.UpdateUser(c.Request.Context(), actorFrom(c), id, in)
	if err != nil {
		fail(c, err)
		return
	}
	c.JSON(http.StatusOK, u)
}

func (s *Server) resetPassword(c *gin.Context) {
	id, ok := idParam(c, "id")
	if !ok {
		return
	}
	var in struct {
		Password string `json:"password"`
	}
	if !bind(c, &in) {
		return
	}
	if err := s.access.ResetPassword(c.Request.Context(), actorFrom(c), id, in.Password); err != nil {
		fail(c, err)
		return
	}
	c.Status(http.StatusNoContent)
}

func (s *Server) deleteSystemUser(c *gin.Context) {
	id, ok := idParam(c, "id")
	if !ok {
		return
	}
	if err := s.access.DeleteUser(c.Request.Context(), actorFrom(c), id); err != nil {
		fail(c, err)
		return
	}
	c.Status(http.StatusNoContent)
}

func (s *Server) setUserBranches(c *gin.Context) {
	id, ok := idParam(c, "id")
	if !ok {
		return
	}
	var in struct {
		BranchIDs []uint64 `json:"branch_ids"`
	}
	if !bind(c, &in) {
		return
	}
	u, err := s.access.SetBranches(c.Request.Context(), actorFrom(c), id, in.BranchIDs)
	if err != nil {
		fail(c, err)
		return
	}
	c.JSON(http.StatusOK, u)
}

// ---------- departments & positions ----------

type orgName struct {
	Name     string `json:"name"`
	BranchID uint64 `json:"branch_id"` // create: the branch it belongs to
}

func (s *Server) listPositions(c *gin.Context) {
	branchID, _ := strconv.ParseUint(c.Query("branch_id"), 10, 64)
	out, err := s.employees.PositionList(c.Request.Context(), actorFrom(c), branchID)
	if err != nil {
		fail(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"items": out})
}

func (s *Server) saveDepartment(c *gin.Context) {
	s.saveOrg(c, s.employees.SaveDepartment)
}

func (s *Server) savePosition(c *gin.Context) {
	s.saveOrg(c, s.employees.SavePosition)
}

// saveOrg creates (POST) or renames (PUT /:id) a department or position.
func (s *Server) saveOrg(c *gin.Context, save func(context.Context, *auth.Actor, uint64, uint64, string) (*employee.OrgItem, error)) {
	var id uint64
	if c.Param("id") != "" {
		var ok bool
		if id, ok = idParam(c, "id"); !ok {
			return
		}
	}
	var in orgName
	if !bind(c, &in) {
		return
	}
	item, err := save(c.Request.Context(), actorFrom(c), id, in.BranchID, in.Name)
	if err != nil {
		fail(c, err)
		return
	}
	status := http.StatusOK
	if id == 0 {
		status = http.StatusCreated
	}
	c.JSON(status, item)
}

func (s *Server) deleteDepartment(c *gin.Context) {
	id, ok := idParam(c, "id")
	if !ok {
		return
	}
	if err := s.employees.DeleteDepartment(c.Request.Context(), actorFrom(c), id); err != nil {
		fail(c, err)
		return
	}
	c.Status(http.StatusNoContent)
}

func (s *Server) deletePosition(c *gin.Context) {
	id, ok := idParam(c, "id")
	if !ok {
		return
	}
	if err := s.employees.DeletePosition(c.Request.Context(), actorFrom(c), id); err != nil {
		fail(c, err)
		return
	}
	c.Status(http.StatusNoContent)
}

// ---------- leave types & balances ----------

func (s *Server) createLeaveType(c *gin.Context) {
	var in leave.TypeInput
	if !bind(c, &in) {
		return
	}
	t, err := s.leave.CreateType(c.Request.Context(), actorFrom(c), in)
	if err != nil {
		fail(c, err)
		return
	}
	c.JSON(http.StatusCreated, t)
}

func (s *Server) updateLeaveType(c *gin.Context) {
	id, ok := idParam(c, "id")
	if !ok {
		return
	}
	var in leave.TypeInput
	if !bind(c, &in) {
		return
	}
	t, err := s.leave.UpdateType(c.Request.Context(), actorFrom(c), id, in)
	if err != nil {
		fail(c, err)
		return
	}
	c.JSON(http.StatusOK, t)
}

func (s *Server) deleteLeaveType(c *gin.Context) {
	id, ok := idParam(c, "id")
	if !ok {
		return
	}
	if err := s.leave.DeleteType(c.Request.Context(), actorFrom(c), id); err != nil {
		fail(c, err)
		return
	}
	c.Status(http.StatusNoContent)
}

func (s *Server) thisYear(c *gin.Context) int {
	today, _ := s.reports.Today(c.Request.Context())
	if y, err := strconv.Atoi(c.Query("year")); err == nil && y >= 2000 && y <= 2100 {
		return y
	}
	return today.Year()
}

func (s *Server) leaveBalances(c *gin.Context) {
	var f leave.BalanceFilter
	if err := c.ShouldBindQuery(&f); err != nil {
		fail(c, apperr.BadRequest("INVALID_INPUT", "Invalid filter."))
		return
	}
	f.Year = s.thisYear(c)
	rows, types, err := s.leave.Balances(c.Request.Context(), actorFrom(c), f)
	if err != nil {
		fail(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"year": f.Year, "items": rows, "types": types})
}

func (s *Server) setLeaveBalance(c *gin.Context) {
	var in leave.BalanceInput
	if !bind(c, &in) {
		return
	}
	if err := s.leave.SetBalance(c.Request.Context(), actorFrom(c), in); err != nil {
		fail(c, err)
		return
	}
	c.Status(http.StatusNoContent)
}

func (s *Server) resetLeaveBalance(c *gin.Context) {
	emp, _ := strconv.ParseUint(c.Query("employee_id"), 10, 64)
	typ, _ := strconv.ParseUint(c.Query("leave_type_id"), 10, 64)
	if err := s.leave.ResetBalance(c.Request.Context(), actorFrom(c), emp, typ, s.thisYear(c)); err != nil {
		fail(c, err)
		return
	}
	c.Status(http.StatusNoContent)
}

func (s *Server) myLeaveBalances(c *gin.Context) {
	year := s.thisYear(c)
	out, err := s.leave.MyBalances(c.Request.Context(), actorFrom(c), year)
	if err != nil {
		fail(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"year": year, "items": out})
}
