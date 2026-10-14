package httpapi

import (
	"context"
	"net/http"
	"strings"

	"attendance/backend/internal/apperr"
	"attendance/backend/internal/branch"
	"attendance/backend/internal/notification"

	"github.com/gin-gonic/gin"
)

func (s *Server) listBranches(c *gin.Context) {
	out, err := s.branches.List(c.Request.Context(), actorFrom(c))
	if err != nil {
		fail(c, err)
		return
	}
	c.JSON(http.StatusOK, paged(c, out))
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
	if !bind(c, &in) || !s.checkBot(c, &in) {
		return
	}
	b, err := s.branches.Create(c.Request.Context(), actorFrom(c), in)
	if err != nil {
		fail(c, err)
		return
	}
	s.botsChanged(in)
	c.JSON(http.StatusCreated, b)
}

// checkBot verifies a new bot token with Telegram and seals it for storing (the
// token itself never reaches the service, the database in clear, or the audit log).
func (s *Server) checkBot(c *gin.Context, in *branch.Input) bool {
	if in.TelegramBotToken == nil {
		return true
	}
	tok := strings.TrimSpace(*in.TelegramBotToken)
	in.TelegramBotToken = nil
	if err := branch.ValidBotToken(tok); err != nil {
		fail(c, err)
		return false
	}
	name := ""
	if tok != "" {
		me, err := s.tg.WithToken(tok).GetMe(c.Request.Context())
		if err != nil {
			fail(c, apperr.BadRequest("TELEGRAM_TOKEN_REJECTED", "Telegram rejected this bot token. Copy it again from @BotFather."))
			return false
		}
		name = me.Username
	}
	sealed, err := s.bots.Seal(tok)
	if err != nil {
		fail(c, err)
		return false
	}
	in.BotSealed, in.BotUsername = &sealed, &name
	return true
}

// botsChanged refreshes the bots and points the menu buttons at the Mini App.
func (s *Server) botsChanged(in branch.Input) {
	if in.BotSealed == nil && in.MiniAppURL == nil && in.TelegramGroupID == nil && in.TelegramReviewChatID == nil {
		return
	}
	s.bots.Invalidate()
	if in.BotSealed != nil || in.MiniAppURL != nil {
		go notification.SyncBots(context.Background(), s.db, s.tg, s.bots)
	}
}

func (s *Server) updateBranch(c *gin.Context) {
	id, ok := idParam(c, "id")
	if !ok {
		return
	}
	var in branch.Input
	if !bind(c, &in) || !s.checkBot(c, &in) {
		return
	}
	b, err := s.branches.Update(c.Request.Context(), actorFrom(c), id, in)
	if err != nil {
		fail(c, err)
		return
	}
	s.botsChanged(in)
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
