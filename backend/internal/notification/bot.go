package notification

import (
	"context"
	"log/slog"
	"time"

	"attendance/backend/internal/settings"
	"attendance/backend/internal/telegram"
)

// SyncBot points the bot's chat menu button at the Mini App and records the
// bot's @username (for t.me links). Runs at start-up and after Telegram settings
// change; failures only log, since notifications work without either.
func SyncBot(ctx context.Context, tg *telegram.Client, st *settings.Service) {
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	cfg, err := st.Get(ctx)
	if err != nil || !cfg.TelegramReady() {
		return
	}
	if me, err := tg.GetMe(ctx); err != nil {
		slog.Warn("telegram getMe failed", "err", err)
	} else if me.Username != cfg.TelegramBotUsername {
		if err := st.SetBotUsername(ctx, me.Username); err != nil {
			slog.Warn("save bot username", "err", err)
		}
	}
	if err := tg.SetMenuButton(ctx, "Open App", cfg.MiniAppURL); err != nil {
		slog.Warn("telegram setChatMenuButton failed", "err", err)
	}
}
