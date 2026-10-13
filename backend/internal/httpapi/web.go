package httpapi

// Static front ends for the single-container image (root Dockerfile, e.g. Railway):
// the API serves the HR admin panel at /admin/ and the Telegram Mini App at /app/
// from WEB_DIR, with the caching and security headers of admin/nginx.conf and
// miniapp/nginx.conf (which do this job under docker compose).

import (
	"io/fs"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"github.com/gin-gonic/gin"
)

type webApp struct {
	name    string            // URL prefix and folder in WEB_DIR
	headers map[string]string // on the HTML shell; "" removes a default header
}

var webApps = []webApp{
	{"admin", map[string]string{
		"X-Frame-Options":    "DENY",
		"Referrer-Policy":    "same-origin",
		"Permissions-Policy": "camera=(), microphone=(), geolocation=()",
		// 'unsafe-inline' scripts: Nuxt inlines its runtime config.
		"Content-Security-Policy": "default-src 'self'; script-src 'self' 'unsafe-inline'; style-src 'self' 'unsafe-inline'; img-src 'self' data:; connect-src 'self'; frame-ancestors 'none'; base-uri 'self'; form-action 'self'",
	}},
	{"app", map[string]string{
		// Telegram Web (web.telegram.org) shows the Mini App in an iframe: CSP frame-ancestors decides.
		"X-Frame-Options":         "",
		"Referrer-Policy":         "no-referrer",
		"Permissions-Policy":      "camera=(self), geolocation=(self), microphone=()",
		"Content-Security-Policy": "default-src 'self'; script-src 'self' 'unsafe-inline' https://telegram.org; style-src 'self' 'unsafe-inline'; img-src 'self' blob: data:; media-src 'self' blob:; connect-src 'self'; frame-ancestors https://web.telegram.org; base-uri 'self'; form-action 'self'",
	}},
}

func mountWeb(r *gin.Engine, dir string) {
	for _, a := range webApps {
		h := a.handler(os.DirFS(filepath.Join(dir, a.name)))
		r.GET("/"+a.name+"/*file", h)
		r.HEAD("/"+a.name+"/*file", h)
		to := "/" + a.name + "/"
		r.GET("/"+a.name, func(c *gin.Context) { c.Redirect(http.StatusMovedPermanently, to) })
	}
	r.GET("/", func(c *gin.Context) { c.Redirect(http.StatusFound, "/admin/") })
}

func (a webApp) handler(fsys fs.FS) gin.HandlerFunc {
	return func(c *gin.Context) {
		name := strings.TrimPrefix(c.Param("file"), "/")
		h := c.Writer.Header()
		// Content-hashed build output never changes.
		if strings.HasPrefix(name, "_nuxt/") {
			if !isFile(fsys, name) {
				c.String(http.StatusNotFound, "Not found")
				return
			}
			h.Set("Cache-Control", "public, max-age=31536000, immutable")
			http.ServeFileFS(c.Writer, c.Request, fsys, name)
			return
		}
		// Real files (favicon…) as they are; every other path gets the SPA shell.
		// Always revalidated so a deploy reaches users at once.
		if !isFile(fsys, name) {
			name = "200.html"
		}
		h.Set("Cache-Control", "no-cache")
		for k, v := range a.headers {
			if v == "" {
				h.Del(k)
			} else {
				h.Set(k, v)
			}
		}
		http.ServeFileFS(c.Writer, c.Request, fsys, name)
	}
}

// isFile: name is a clean relative path to a regular file (no directories, no "..").
func isFile(fsys fs.FS, name string) bool {
	if !fs.ValidPath(name) || name == "." {
		return false
	}
	st, err := fs.Stat(fsys, name)
	return err == nil && st.Mode().IsRegular()
}
