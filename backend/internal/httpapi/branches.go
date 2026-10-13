package httpapi

import (
	"net/http"

	"attendance/backend/internal/branch"

	"github.com/gin-gonic/gin"
)

func (s *Server) listBranches(c *gin.Context) {
	out, err := s.branches.List(c.Request.Context(), actorFrom(c))
	if err != nil {
		fail(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"items": out})
}

func (s *Server) getBranch(c *gin.Context) {
	id, ok := idParam(c, "id")
	if !ok {
		return
	}
	b, err := s.branches.Get(c.Request.Context(), actorFrom(c), id)
	if err != nil {
		fail(c, err)
		return
	}
	c.JSON(http.StatusOK, b)
}

func (s *Server) createBranch(c *gin.Context) {
	var in branch.Input
	if !bind(c, &in) {
		return
	}
	b, err := s.branches.Create(c.Request.Context(), actorFrom(c), in)
	if err != nil {
		fail(c, err)
		return
	}
	c.JSON(http.StatusCreated, b)
}

func (s *Server) updateBranch(c *gin.Context) {
	id, ok := idParam(c, "id")
	if !ok {
		return
	}
	var in branch.Input
	if !bind(c, &in) {
		return
	}
	b, err := s.branches.Update(c.Request.Context(), actorFrom(c), id, in)
	if err != nil {
		fail(c, err)
		return
	}
	c.JSON(http.StatusOK, b)
}

// assignBranchAdmin: {"employee_id": 12} assigns, {"employee_id": null} removes the admin.
func (s *Server) assignBranchAdmin(c *gin.Context) {
	id, ok := idParam(c, "id")
	if !ok {
		return
	}
	var in struct {
		EmployeeID *uint64 `json:"employee_id"`
	}
	if !bind(c, &in) {
		return
	}
	b, err := s.branches.AssignAdmin(c.Request.Context(), actorFrom(c), id, in.EmployeeID)
	if err != nil {
		fail(c, err)
		return
	}
	c.JSON(http.StatusOK, b)
}
