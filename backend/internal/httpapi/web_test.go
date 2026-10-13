package httpapi

import (
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"attendance/backend/internal/config"

	"github.com/gin-gonic/gin"
)

func TestWebFrontEnds(t *testing.T) {
	dir := t.TempDir()
	files := map[string]string{
		"admin/200.html": "admin shell", "admin/favicon.svg": "<svg/>", "admin/_nuxt/entry.abc.js": "console.log(1)",
		"app/200.html": "app shell", "app/_nuxt/entry.def.js": "console.log(2)", "secret.txt": "outside",
	}
	for name, body := range files {
		os.MkdirAll(filepath.Dir(filepath.Join(dir, name)), 0o755)
		os.WriteFile(filepath.Join(dir, name), []byte(body), 0o644)
	}
	r := (&Server{cfg: &config.Config{WebDir: dir, TrustedProxies: []string{"100.64.0.0/10"}}}).Router()
	get := func(method, path string) *httptest.ResponseRecorder {
		w := httptest.NewRecorder()
		r.ServeHTTP(w, httptest.NewRequest(method, path, nil))
		return w
	}
	cases := []struct {
		path, body, cache, ctype string
		code                     int
	}{
		{"/admin/", "admin shell", "no-cache", "text/html", 200},
		{"/admin/employees/12", "admin shell", "no-cache", "text/html", 200},
		{"/admin/favicon.svg", "<svg/>", "no-cache", "image/svg+xml", 200},
		{"/admin/_nuxt/entry.abc.js", "console.log(1)", "public, max-age=31536000, immutable", "text/javascript", 200},
		{"/admin/_nuxt/missing.js", "Not found", "", "text/plain", 404},
		{"/admin/_nuxt/", "Not found", "", "text/plain", 404},
		{"/admin/../secret.txt", "invalid URL path", "", "text/plain", 400},     // never the file outside
		{"/admin/%2e%2e/secret.txt", "invalid URL path", "", "text/plain", 400}, // never the file outside
		{"/app/", "app shell", "no-cache", "text/html", 200},
		{"/app/leave/new", "app shell", "no-cache", "text/html", 200},
		{"/app/_nuxt/entry.def.js", "console.log(2)", "public, max-age=31536000, immutable", "text/javascript", 200},
	}
	for _, tc := range cases {
		w := get("GET", tc.path)
		if w.Code != tc.code || strings.TrimSpace(w.Body.String()) != tc.body || w.Header().Get("Cache-Control") != tc.cache ||
			!strings.HasPrefix(w.Header().Get("Content-Type"), tc.ctype) {
			t.Errorf("GET %s: %d %q cache=%q type=%q", tc.path, w.Code, w.Body.String(), w.Header().Get("Cache-Control"), w.Header().Get("Content-Type"))
		}
	}

	admin, app := get("GET", "/admin/").Header(), get("GET", "/app/").Header()
	if admin.Get("X-Frame-Options") != "DENY" || !strings.Contains(admin.Get("Content-Security-Policy"), "frame-ancestors 'none'") {
		t.Errorf("admin headers: %v", admin)
	}
	if app.Get("X-Frame-Options") != "" || !strings.Contains(app.Get("Content-Security-Policy"), "frame-ancestors https://web.telegram.org") ||
		!strings.Contains(app.Get("Content-Security-Policy"), "https://telegram.org") || app.Get("Permissions-Policy") != "camera=(self), geolocation=(self), microphone=()" {
		t.Errorf("Mini App headers: %v", app)
	}
	if w := get("HEAD", "/app/"); w.Code != 200 {
		t.Errorf("HEAD /app/: %d", w.Code)
	}
	for path, loc := range map[string]string{"/": "/admin/", "/admin": "/admin/", "/app": "/app/"} {
		if w := get("GET", path); (w.Code != 301 && w.Code != 302) || w.Header().Get("Location") != loc {
			t.Errorf("GET %s: %d → %q", path, w.Code, w.Header().Get("Location"))
		}
	}
	if w := get("GET", "/nothing"); w.Code != 404 {
		t.Errorf("GET /nothing: %d", w.Code)
	}

	// Client IP comes from X-Forwarded-For only when the connection is from a trusted proxy.
	var seen string
	r.GET("/test-ip", func(c *gin.Context) { seen = c.ClientIP() })
	for remote, want := range map[string]string{"100.64.3.4:5000": "203.0.113.9", "198.51.100.7:5000": "198.51.100.7"} {
		req := httptest.NewRequest("GET", "/test-ip", nil)
		req.RemoteAddr = remote
		req.Header.Set("X-Forwarded-For", "203.0.113.9")
		r.ServeHTTP(httptest.NewRecorder(), req)
		if seen != want {
			t.Errorf("from %s: client IP %s, want %s", remote, seen, want)
		}
	}

	// On a platform whose edge sets the visitor's IP in a header (Railway: X-Real-IP).
	pr := (&Server{cfg: &config.Config{ClientIPHeader: "X-Real-IP"}}).Router()
	pr.GET("/test-ip", func(c *gin.Context) { seen = c.ClientIP() })
	for hdr, want := range map[string]string{"203.0.113.50": "203.0.113.50", " 2001:db8::7 ": "2001:db8::7", "junk, 1.2.3.4": "152.233.15.121", "": "152.233.15.121"} {
		req := httptest.NewRequest("GET", "/test-ip", nil)
		req.RemoteAddr = "152.233.15.121:4000"
		req.Header.Set("X-Forwarded-For", "198.51.100.1") // ignored: the edge is not a trusted proxy
		if hdr != "" {
			req.Header.Set("X-Real-IP", hdr)
		}
		pr.ServeHTTP(httptest.NewRecorder(), req)
		if seen != want {
			t.Errorf("X-Real-IP %q: client IP %s, want %s", hdr, seen, want)
		}
	}
}
