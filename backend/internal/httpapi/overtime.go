package httpapi

import (
	"net/http"

	"attendance/backend/internal/overtime"

	"github.com/gin-gonic/gin"
)

// ---------- overtime: admin panel and reviewers ----------

// listOvertime pages in the database: requests keep growing.
func (s *Server) listOvertime(c *gin.Context) {
	var f overtime.ListFilter
	_ = c.ShouldBindQuery(&f)
	page, size := pageParams(c)
	out, total, err := s.overtime.Page(c.Request.Context(), actorFrom(c), f, page, size)
	if err != nil {
		fail(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"items": out, "total": total, "page": page, "page_size": size})
}

func (s *Server) pendingOvertime(c *gin.Context) {
	out, err := s.overtime.PendingForReviewer(c.Request.Context(), actorFrom(c))
	if err != nil {
		fail(c, err)
		return
	}
	c.JSON(http.StatusOK, paged(c, out))
}

func (s *Server) getOvertime(c *gin.Context) {
	id, ok := idParam(c, "id")
	if !ok {
		return
	}
	out, err := s.overtime.Get(c.Request.Context(), actorFrom(c), id)
	if err != nil {
		fail(c, err)
		return
	}
	c.JSON(http.StatusOK, out)
}

func (s *Server) reviewOvertime(approve bool) gin.HandlerFunc {
	return func(c *gin.Context) {
		id, ok := idParam(c, "id")
		if !ok {
			return
		}
		var body struct {
			Note string `json:"note"`
		}
		_ = c.ShouldBindJSON(&body)
		out, err := s.overtime.Review(c.Request.Context(), actorFrom(c), id, approve, body.Note)
		if err != nil {
			fail(c, err)
			return
		}
		c.JSON(http.StatusOK, out)
	}
}

// ---------- overtime: the employee (Mini App) ----------

// appOvertimePlan: the person's shift on ?date= and the suggested overtime.
func (s *Server) appOvertimePlan(c *gin.Context) {
	out, err := s.overtime.Plan(c.Request.Context(), actorFrom(c), c.Query("date"))
	if err != nil {
		fail(c, err)
		return
	}
	c.JSON(http.StatusOK, out)
}

func (s *Server) appMyOvertime(c *gin.Context) {
	out, err := s.overtime.Mine(c.Request.Context(), actorFrom(c))
	if err != nil {
		fail(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"items": out})
}

func (s *Server) appOvertimeSubmit(c *gin.Context) {
	var in overtime.SubmitInput
	if !bind(c, &in) {
		return
	}
	out, err := s.overtime.Submit(c.Request.Context(), actorFrom(c), in)
	if err != nil {
		fail(c, err)
		return
	}
	c.JSON(http.StatusCreated, out)
}

func (s *Server) appOvertimeCancel(c *gin.Context) {
	id, ok := idParam(c, "id")
	if !ok {
		return
	}
	out, err := s.overtime.Cancel(c.Request.Context(), actorFrom(c), id)
	if err != nil {
		fail(c, err)
		return
	}
	c.JSON(http.StatusOK, out)
}
