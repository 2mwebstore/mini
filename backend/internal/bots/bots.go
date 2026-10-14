// Package bots gives the rest of the API each branch's Telegram bot: its token, its
// @username, the Mini App URL and the branch's chats. Every branch has its own bot
// (set on the branch page); the token is stored encrypted on the branch row.
package bots

import (
	"context"
	"log/slog"
	"sort"
	"sync"
	"time"

	"attendance/backend/internal/models"
	"attendance/backend/internal/secret"

	"gorm.io/gorm"
)

// Bot is one branch's Telegram setup.
type Bot struct {
	BranchID     uint64
	BranchName   string
	Token        string // decrypted
	Username     string
	MiniAppURL   string
	GroupID      int64 // daily report
	ReviewChatID int64 // 0 = DM the reviewers
}

type Registry struct {
	db  *gorm.DB
	box *secret.Box

	mu   sync.RWMutex
	bots map[uint64]*Bot // branches with a bot token
	at   time.Time
}

// ttl: other API replicas pick up a branch's new bot within this time.
const ttl = 30 * time.Second

func New(db *gorm.DB, box *secret.Box) *Registry { return &Registry{db: db, box: box} }

// Seal encrypts a bot token for storing on the branch ("" stays "").
func (r *Registry) Seal(token string) (string, error) {
	if token == "" {
		return "", nil
	}
	return r.box.Seal(token)
}

// Invalidate makes the next lookup re-read the branches (after a branch's Telegram changes).
func (r *Registry) Invalidate() {
	r.mu.Lock()
	r.bots = nil
	r.mu.Unlock()
}

func (r *Registry) load(ctx context.Context) map[uint64]*Bot {
	r.mu.RLock()
	if r.bots != nil && time.Since(r.at) < ttl {
		m := r.bots
		r.mu.RUnlock()
		return m
	}
	r.mu.RUnlock()
	var bs []models.Branch
	if err := r.db.WithContext(ctx).Where("telegram_bot_token <> ''").Find(&bs).Error; err != nil {
		slog.Error("bots: load branches", "err", err)
		return map[uint64]*Bot{}
	}
	m := make(map[uint64]*Bot, len(bs))
	for _, b := range bs {
		tok, err := r.box.Open(b.TelegramBotToken)
		if err != nil || tok == "" {
			slog.Warn("bots: a branch's bot token is unreadable (JWT_SECRET changed?); set it again on the branch page", "branch", b.ID)
			continue
		}
		m[b.ID] = &Bot{BranchID: b.ID, BranchName: b.Name, Token: tok, Username: b.TelegramBotUsername, MiniAppURL: b.MiniAppURL,
			GroupID: b.TelegramGroupID, ReviewChatID: b.TelegramReviewChatID}
	}
	r.mu.Lock()
	r.bots, r.at = m, time.Now()
	r.mu.Unlock()
	return m
}

// ForBranch is the bot of a branch (nil: none set, or no branch).
func (r *Registry) ForBranch(ctx context.Context, branchID *uint64) *Bot {
	if branchID == nil {
		return nil
	}
	return r.load(ctx)[*branchID]
}

// All lists every branch's bot, by branch.
func (r *Registry) All(ctx context.Context) []*Bot {
	m := r.load(ctx)
	out := make([]*Bot, 0, len(m))
	for _, b := range m {
		out = append(out, b)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].BranchID < out[j].BranchID })
	return out
}

// Tokens lists the distinct bot tokens (to check a Mini App launch against).
func (r *Registry) Tokens(ctx context.Context) []string {
	seen := map[string]bool{}
	out := []string{}
	for _, b := range r.All(ctx) {
		if !seen[b.Token] {
			seen[b.Token] = true
			out = append(out, b.Token)
		}
	}
	return out
}

// Any returns some branch's bot, preferring the given branch: for messages that
// belong to no branch (e.g. backup alerts to the owner).
func (r *Registry) Any(ctx context.Context, prefer *uint64) *Bot {
	if b := r.ForBranch(ctx, prefer); b != nil {
		return b
	}
	if all := r.All(ctx); len(all) > 0 {
		return all[0]
	}
	return nil
}
