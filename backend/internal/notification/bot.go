package notification

import (
	"context"
	"log/slog"
	"time"

	"attendance/backend/internal/bots"
	"attendance/backend/internal/models"
	"attendance/backend/internal/telegram"

	"gorm.io/gorm"
)

// SyncBots points each branch bot's chat menu button at the Mini App and records
// the bot's @username (for t.me links). Runs at start-up and after a branch's
// Telegram changes; failures only log, since notifications work without either.
func SyncBots(ctx context.Context, db *gorm.DB, tg *telegram.Client, reg *bots.Registry) {
	ctx, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()
	changed := false
	for _, b := range reg.All(ctx) {
		c := tg.WithToken(b.Token)
		if me, err := c.GetMe(ctx); err != nil {
			slog.Warn("telegram getMe failed", "branch", b.BranchID, "err", err)
		} else if me.Username != b.Username {
			if err := db.WithContext(ctx).Model(&models.Branch{}).Where("id = ?", b.BranchID).Update("telegram_bot_username", me.Username).Error; err != nil {
				slog.Warn("save bot username", "branch", b.BranchID, "err", err)
			}
			changed = true
		}
		if b.MiniAppURL != "" {
			if err := c.SetMenuButton(ctx, "Open App", b.MiniAppURL); err != nil {
				slog.Warn("telegram setChatMenuButton failed", "branch", b.BranchID, "err", err)
			}
		}
	}
	if changed {
		reg.Invalidate()
	}
}
