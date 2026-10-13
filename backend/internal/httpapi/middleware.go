package httpapi

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"strconv"
	"strings"
	"time"

	"attendance/backend/internal/apperr"
	"attendance/backend/internal/auth"

	"github.com/gin-gonic/gin"
	"github.com/redis/go-redis/v9"
)

const actorKey = "actor"

func actorFrom(c *gin.Context) *auth.Actor {
	a, _ := c.Get(actorKey)
	actor, _ := a.(*auth.Actor)
	return actor
}

func fail(c *gin.Context, err error) {
	e := apperr.As(err)
	if e.Status >= 500 {
		slog.Error("request failed", "path", c.FullPath(), "request_id", c.GetString("request_id"), "err", err)
	}
	c.AbortWithStatusJSON(e.Status, gin.H{"error": e})
}

func requestID() gin.HandlerFunc {
	return func(c *gin.Context) {
		id := c.GetHeader("X-Request-ID")
		if id == "" || len(id) > 64 {
			b := make([]byte, 8)
			_, _ = rand.Read(b)
			id = hex.EncodeToString(b)
		}
		c.Set("request_id", id)
		c.Header("X-Request-ID", id)
		c.Next()
	}
}

func accessLog() gin.HandlerFunc {
	return func(c *gin.Context) {
		start := time.Now()
		c.Next()
		if c.FullPath() == "/health" {
			return
		}
		slog.Info("http", "method", c.Request.Method, "path", c.FullPath(), "status", c.Writer.Status(),
			"ms", time.Since(start).Milliseconds(), "ip", c.ClientIP(), "request_id", c.GetString("request_id"))
	}
}

// clientIPHeader keeps the platform's client-IP header only when it holds one IP
// address, so ClientIP() falls back to the connection rather than trusting junk.
func clientIPHeader(name string) gin.HandlerFunc {
	return func(c *gin.Context) {
		if v := c.Request.Header.Get(name); v != "" {
			if ip := net.ParseIP(strings.TrimSpace(v)); ip != nil {
				c.Request.Header.Set(name, ip.String())
			} else {
				c.Request.Header.Del(name)
			}
		}
		c.Next()
	}
}

func securityHeaders() gin.HandlerFunc {
	return func(c *gin.Context) {
		h := c.Writer.Header()
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("X-Frame-Options", "DENY")
		h.Set("Referrer-Policy", "no-referrer")
		c.Next()
	}
}

// bodyLimit caps request bodies at n bytes; routes listed in larger (photo uploads) get their own cap.
func bodyLimit(n int64, larger map[string]int64) gin.HandlerFunc {
	return func(c *gin.Context) {
		limit := n
		if v, ok := larger[c.FullPath()]; ok {
			limit = v
		}
		c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, limit)
		c.Next()
	}
}

func cors(origins []string) gin.HandlerFunc {
	allowed := map[string]bool{}
	for _, o := range origins {
		allowed[o] = true
	}
	return func(c *gin.Context) {
		origin := c.GetHeader("Origin")
		if origin != "" && allowed[origin] {
			h := c.Writer.Header()
			h.Set("Access-Control-Allow-Origin", origin)
			h.Set("Vary", "Origin")
			h.Set("Access-Control-Allow-Headers", "Authorization, Content-Type, X-Request-ID")
			h.Set("Access-Control-Allow-Methods", "GET, POST, PUT, DELETE, OPTIONS")
			h.Set("Access-Control-Max-Age", "600")
		}
		if c.Request.Method == http.MethodOptions {
			c.AbortWithStatus(http.StatusNoContent)
			return
		}
		c.Next()
	}
}

// rateLimit is a fixed-window limiter in Redis. keyFn picks the identity (IP, user...).
// If Redis is unavailable the request is allowed (fail-open) and logged.
func rateLimit(rdb *redis.Client, name string, limit int, window time.Duration, keyFn func(*gin.Context) string) gin.HandlerFunc {
	return func(c *gin.Context) {
		bucket := time.Now().Unix() / int64(window.Seconds())
		key := fmt.Sprintf("rl:%s:%s:%d", name, keyFn(c), bucket)
		n, err := rdb.Incr(c.Request.Context(), key).Result()
		if err != nil {
			slog.Warn("rate limiter unavailable", "err", err)
			c.Next()
			return
		}
		if n == 1 {
			rdb.Expire(c.Request.Context(), key, window+time.Second)
		}
		if n > int64(limit) {
			c.Header("Retry-After", strconv.Itoa(int(window.Seconds())))
			fail(c, apperr.New(http.StatusTooManyRequests, "RATE_LIMITED", "Too many requests. Please wait a moment."))
			return
		}
		c.Next()
	}
}

// sessionCookie carries the admin panel's JWT: HttpOnly (scripts can't read it),
// SameSite=Strict, scoped to the API.
const sessionCookie = "att_session"

func (s *Server) setSession(c *gin.Context, token string, maxAge int) {
	http.SetCookie(c.Writer, &http.Cookie{Name: sessionCookie, Value: token, Path: "/api/", MaxAge: maxAge,
		HttpOnly: true, Secure: s.cfg.IsProduction(), SameSite: http.SameSiteStrictMode})
}

// jwtAuth authenticates admin panel requests: the session cookie, or a Bearer
// token for API clients. Cookie-authenticated writes must also send
// X-Requested-With, which a cross-site form or image can't (CSRF defence on top
// of SameSite=Strict).
func (s *Server) jwtAuth() gin.HandlerFunc {
	return func(c *gin.Context) {
		token, ok := strings.CutPrefix(c.GetHeader("Authorization"), "Bearer ")
		if !ok {
			ck, err := c.Cookie(sessionCookie)
			if err != nil || ck == "" {
				fail(c, apperr.ErrUnauthorized)
				return
			}
			if m := c.Request.Method; m != http.MethodGet && m != http.MethodHead && c.GetHeader("X-Requested-With") == "" {
				fail(c, apperr.Forbidden("Missing X-Requested-With header."))
				return
			}
			token = ck
		}
		actor, err := s.auth.ActorFromJWT(c.Request.Context(), token)
		if err != nil {
			fail(c, err)
			return
		}
		actor.IP = c.ClientIP()
		c.Set(actorKey, actor)
		c.Next()
	}
}

const webAppUserKey = "webapp_user"

// webAppAuth authenticates the Telegram Mini App ("Authorization: tma <initData>").
// The signed initData yields the Telegram user ID, which resolves to the linked
// employee, so registration, deactivation and roles apply as in the admin panel.
func (s *Server) webAppAuth() gin.HandlerFunc {
	return func(c *gin.Context) {
		raw, ok := strings.CutPrefix(c.GetHeader("Authorization"), "tma ")
		if !ok {
			fail(c, apperr.ErrUnauthorized)
			return
		}
		st, err := s.settings.Get(c.Request.Context())
		if err != nil {
			fail(c, err)
			return
		}
		if !st.TelegramReady() {
			fail(c, apperr.New(http.StatusServiceUnavailable, "TELEGRAM_NOT_CONFIGURED", "This app isn't set up yet. Ask your administrator to add the Telegram bot in Settings."))
			return
		}
		init, err := auth.ValidateWebAppInitData(raw, st.TelegramBotToken, s.cfg.MiniAppSessionTTL, time.Now())
		if err != nil {
			fail(c, err)
			return
		}
		actor, err := s.auth.ActorFromTelegram(c.Request.Context(), init.User.ID)
		if err != nil {
			fail(c, err)
			return
		}
		actor.Channel, actor.IP = auth.ChannelMiniApp, c.ClientIP()
		c.Set(actorKey, actor)
		c.Set(webAppUserKey, init.User)
		c.Next()
	}
}

func require(p auth.Permission) gin.HandlerFunc {
	return func(c *gin.Context) {
		if a := actorFrom(c); a == nil || !a.Has(p) {
			fail(c, apperr.ErrForbidden)
			return
		}
		c.Next()
	}
}

func requireReviewer() gin.HandlerFunc {
	return func(c *gin.Context) {
		if a := actorFrom(c); a == nil || !a.IsReviewer() {
			fail(c, apperr.ErrForbidden)
			return
		}
		c.Next()
	}
}

func idParam(c *gin.Context, name string) (uint64, bool) {
	id, err := strconv.ParseUint(c.Param(name), 10, 64)
	if err != nil || id == 0 {
		fail(c, apperr.BadRequest("INVALID_ID", "Invalid id."))
		return 0, false
	}
	return id, true
}

func bind(c *gin.Context, v any) bool {
	if err := c.ShouldBindJSON(v); err != nil {
		fail(c, apperr.BadRequest("INVALID_INPUT", "Invalid request: "+cleanBindError(err)))
		return false
	}
	return true
}

func cleanBindError(err error) string {
	s := err.Error()
	if len(s) > 200 {
		s = s[:200]
	}
	return s
}
