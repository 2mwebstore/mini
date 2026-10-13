package httpapi

// Integration test: real MySQL + Redis, fake Telegram, in-memory photo storage.
// Drives the two real clients' APIs: the Telegram Mini App (/api/v1/app, signed
// initData) and the HR admin panel (/api/v1, session cookie or Bearer JWT).
// Runs only when TEST_DATABASE_URL is set (a MySQL user allowed to create databases):
//
//	TEST_DATABASE_URL='root:pass@tcp(127.0.0.1:3306)/' TEST_REDIS_URL='redis://127.0.0.1:6379/15' go test ./internal/httpapi -run Integration -v

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"sort"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"attendance/backend/internal/attendance"
	"attendance/backend/internal/auth"
	"attendance/backend/internal/branch"
	"attendance/backend/internal/config"
	"attendance/backend/internal/database"
	"attendance/backend/internal/employee"
	"attendance/backend/internal/leave"
	"attendance/backend/internal/models"
	"attendance/backend/internal/notification"
	"attendance/backend/internal/report"
	"attendance/backend/internal/scheduler"
	"attendance/backend/internal/secret"
	"attendance/backend/internal/settings"
	"attendance/backend/internal/telegram"

	mysqldrv "github.com/go-sql-driver/mysql"
	"github.com/redis/go-redis/v9"
)

// ---------- fakes ----------

type memStore struct {
	mu   sync.Mutex
	objs map[string][]byte
}

func (m *memStore) Put(_ context.Context, key string, data []byte, _ string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.objs[key] = append([]byte(nil), data...)
	return nil
}
func (m *memStore) Get(_ context.Context, key string) (io.ReadCloser, int64, string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	d, ok := m.objs[key]
	if !ok {
		return nil, 0, "", fmt.Errorf("not found")
	}
	return io.NopCloser(bytes.NewReader(d)), int64(len(d)), "image/jpeg", nil
}
func (m *memStore) Ping(context.Context) error { return nil }
func (m *memStore) URI(k string) string        { return "mem://" + k }

// fakeTelegram records Bot API calls (JSON or multipart) and answers like Telegram.
type fakeTelegram struct {
	mu   sync.Mutex
	sent []map[string]any
	srv  *httptest.Server
}

var jpeg = append([]byte{0xFF, 0xD8, 0xFF, 0xE0, 0, 0x10, 'J', 'F', 'I', 'F', 0}, bytes.Repeat([]byte{0x42}, 512)...)

// photo returns distinct JPEG bytes per name, like real camera shots.
func photo(name string) string { return string(append(append([]byte(nil), jpeg...), name...)) }

func newFakeTelegram() *fakeTelegram {
	f := &fakeTelegram{}
	f.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		method := r.URL.Path[strings.LastIndex(r.URL.Path, "/")+1:]
		if strings.Contains(r.URL.Path, "Bad") {
			fmt.Fprint(w, `{"ok":false,"error_code":401,"description":"Unauthorized"}`)
			return
		}
		body := map[string]any{}
		upload := strings.HasPrefix(r.Header.Get("Content-Type"), "multipart/")
		if upload {
			if err := r.ParseMultipartForm(32 << 20); err == nil {
				for k, v := range r.MultipartForm.Value {
					body[k] = v[0]
				}
			}
			body["_upload"] = true
		} else {
			_ = json.NewDecoder(r.Body).Decode(&body)
		}
		if id, err := strconv.ParseFloat(fmt.Sprint(body["chat_id"]), 64); err == nil {
			body["chat_id"] = id
		}
		body["_method"] = method
		f.mu.Lock()
		f.sent = append(f.sent, body)
		f.mu.Unlock()
		if method == "getMe" {
			fmt.Fprint(w, `{"ok":true,"result":{"id":42,"is_bot":true,"username":"att_test_bot"}}`)
			return
		}
		if method == "sendPhoto" && upload {
			fmt.Fprint(w, `{"ok":true,"result":{"message_id":1,"photo":[{"file_id":"tg-small","file_unique_id":"us"},{"file_id":"tg-uploaded","file_unique_id":"uu"}]}}`)
			return
		}
		fmt.Fprint(w, `{"ok":true,"result":{"message_id":1}}`)
	}))
	return f
}

// calls returns recorded calls of method (to chat, if non-zero), in order.
func (f *fakeTelegram) calls(method string, chat int64) []map[string]any {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []map[string]any
	for _, s := range f.sent {
		if s["_method"] == method && (chat == 0 || s["chat_id"] == float64(chat)) {
			out = append(out, s)
		}
	}
	return out
}

func markup(call map[string]any) string {
	if call == nil {
		return ""
	}
	if s, ok := call["reply_markup"].(string); ok {
		return s
	}
	b, _ := json.Marshal(call["reply_markup"])
	return string(b)
}

// ---------- harness ----------

type harness struct {
	t     *testing.T
	srv   http.Handler
	db    *sql.DB
	clock *time.Time
}

// botToken is configured through Settings in the test, like an owner would.
const botToken = "123456789:AAHdqTcvCH1vGWJxfSeofSAs0K5PALDsaw"

// initData signs Mini App launch data the way Telegram does (independent of the
// code under test, so a wrong derivation in auth would fail here).
func initData(token string, tgUser int64, authDate time.Time) string {
	q := url.Values{}
	q.Set("auth_date", fmt.Sprint(authDate.Unix()))
	q.Set("query_id", "AAH-test")
	q.Set("user", fmt.Sprintf(`{"id":%d,"first_name":"Test","username":"user%d"}`, tgUser, tgUser))
	keys := make([]string, 0, len(q))
	for k := range q {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	lines := make([]string, len(keys))
	for i, k := range keys {
		lines[i] = k + "=" + q.Get(k)
	}
	secret := hmac.New(sha256.New, []byte("WebAppData"))
	secret.Write([]byte(token))
	mac := hmac.New(sha256.New, secret.Sum(nil))
	mac.Write([]byte(strings.Join(lines, "\n")))
	q.Set("hash", hex.EncodeToString(mac.Sum(nil)))
	return q.Encode()
}

type reqOpt func(*http.Request)

func withHeader(k, v string) reqOpt { return func(r *http.Request) { r.Header.Set(k, v) } }
func withCookie(c *http.Cookie) reqOpt {
	return func(r *http.Request) { r.AddCookie(c) }
}

// do sends a request. body is JSON, or a map[string]string form whose "photo"
// field is sent as a file (multipart).
func (h *harness) do(method, path string, body any, opts ...reqOpt) (*httptest.ResponseRecorder, map[string]any) {
	h.t.Helper()
	var rdr io.Reader
	ctype := "application/json"
	switch b := body.(type) {
	case nil:
	case map[string]string:
		var buf bytes.Buffer
		mw := multipart.NewWriter(&buf)
		for k, v := range b {
			if k == "photo" {
				fw, _ := mw.CreateFormFile("photo", "photo.jpg")
				fw.Write([]byte(v))
				continue
			}
			mw.WriteField(k, v)
		}
		mw.Close()
		rdr, ctype = &buf, mw.FormDataContentType()
	default:
		j, _ := json.Marshal(b)
		rdr = bytes.NewReader(j)
	}
	req := httptest.NewRequest(method, path, rdr)
	req.Header.Set("Content-Type", ctype)
	for _, o := range opts {
		o(req)
	}
	w := httptest.NewRecorder()
	h.srv.ServeHTTP(w, req)
	var out map[string]any
	_ = json.Unmarshal(w.Body.Bytes(), &out)
	return w, out
}

// app calls the Mini App API as Telegram user tgUser.
func (h *harness) app(method, path string, tgUser int64, body any) (int, map[string]any, []byte) {
	h.t.Helper()
	w, out := h.do(method, "/api/v1/app"+path, body, withHeader("Authorization", "tma "+initData(botToken, tgUser, time.Now())))
	return w.Code, out, w.Body.Bytes()
}

// admin calls the admin panel API with a Bearer JWT.
func (h *harness) admin(method, path, jwt string, body any) (int, map[string]any, []byte) {
	h.t.Helper()
	w, out := h.do(method, "/api/v1"+path, body, withHeader("Authorization", "Bearer "+jwt))
	return w.Code, out, w.Body.Bytes()
}

func (h *harness) login(email, password string) (string, *http.Cookie) {
	h.t.Helper()
	w, body := h.do("POST", "/api/v1/auth/login", map[string]any{"email": email, "password": password})
	if w.Code != 200 {
		h.t.Fatalf("login %s: %d %v", email, w.Code, body)
	}
	var session *http.Cookie
	for _, c := range w.Result().Cookies() {
		if c.Name == sessionCookie {
			session = c
		}
	}
	return body["token"].(string), session
}

func (h *harness) set(local string) {
	t, err := time.ParseInLocation("2006-01-02 15:04", local, time.FixedZone("ICT", 7*3600))
	if err != nil {
		h.t.Fatal(err)
	}
	*h.clock = t.UTC()
}

func errCode(m map[string]any) string {
	if e, ok := m["error"].(map[string]any); ok {
		return fmt.Sprint(e["code"])
	}
	return ""
}

func num(m map[string]any, path ...string) float64 {
	var v any = m
	for _, p := range path {
		mm, _ := v.(map[string]any)
		v = mm[p]
	}
	f, _ := v.(float64)
	return f
}

func TestIntegration(t *testing.T) {
	base := os.Getenv("TEST_DATABASE_URL")
	if base == "" {
		t.Skip("TEST_DATABASE_URL not set")
	}
	ctx := context.Background()

	// isolated database per run
	cfg, err := mysqldrv.ParseDSN(base)
	if err != nil {
		t.Fatal(err)
	}
	rb := make([]byte, 4)
	rand.Read(rb)
	dbName := "att_test_" + hex.EncodeToString(rb)
	cfg.DBName = ""
	adminDB, err := sql.Open("mysql", cfg.FormatDSN())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := adminDB.Exec("CREATE DATABASE " + dbName + " CHARACTER SET utf8mb4"); err != nil {
		t.Fatal(err)
	}
	defer adminDB.Exec("DROP DATABASE " + dbName)
	cfg.DBName = dbName
	dsn := cfg.FormatDSN()
	if _, err := database.Migrate(ctx, dsn); err != nil {
		t.Fatal(err)
	}
	gdb, err := database.Open(dsn, false)
	if err != nil {
		t.Fatal(err)
	}
	rawDB, _ := gdb.DB()

	ropt, _ := redis.ParseURL(envOr("TEST_REDIS_URL", "redis://127.0.0.1:6379/15"))
	rdb := redis.NewClient(ropt)
	rdb.FlushDB(ctx)

	ftg := newFakeTelegram()
	defer ftg.srv.Close()
	clock := time.Now().UTC()
	now := func() time.Time { return clock }

	const appURL = "https://attendance.example.test/app/"
	conf := &config.Config{Env: "test", JWTSecret: strings.Repeat("j", 40), JWTTTL: time.Hour, MiniAppSessionTTL: 24 * time.Hour}
	box, _ := secret.New(conf.JWTSecret)
	st := settings.NewService(gdb, box)
	tgc := telegram.New(func(ctx context.Context) string {
		s, _ := st.Get(ctx)
		return s.TelegramBotToken
	}, ftg.srv.URL)
	notifier := notification.New(gdb, tgc, st).Sync()
	store := &memStore{objs: map[string][]byte{}}
	authSvc := auth.NewService(gdb, conf.JWTSecret, conf.JWTTTL)
	empSvc := employee.NewService(gdb)
	attSvc := attendance.NewService(gdb, st, store, notifier)
	attSvc.Now = now
	leaveSvc := leave.NewService(gdb, st, notifier)
	leaveSvc.Now = now
	repSvc := report.NewService(gdb, st)
	repSvc.Now = now
	srv := New(conf, Deps{DB: gdb, Redis: rdb, Store: store, Auth: authSvc, Settings: st, Employees: empSvc,
		Branches: branch.NewService(gdb), Attendance: attSvc, Leave: leaveSvc, Reports: repSvc, Telegram: tgc})
	h := &harness{t: t, srv: srv.Router(), db: rawDB, clock: &clock}

	// ---------- people ----------
	cli := &auth.Actor{Employee: models.Employee{RoleID: models.RoleSuperAdmin}, Channel: auth.ChannelCLI}
	var mainBranch uint64
	h.db.QueryRow("SELECT id FROM branches WHERE code = 'MAIN'").Scan(&mainBranch)
	if mainBranch == 0 {
		t.Fatal("migration 0003 must create the MAIN branch")
	}
	mk := func(code, name, role string, tg int64, mgr *uint64) *models.Employee {
		e, err := empSvc.Create(ctx, cli, employee.Input{EmployeeCode: code, FullName: name, Role: role, TelegramUserID: &tg, ManagerID: mgr,
			Department: "Development", BranchID: &mainBranch})
		if err != nil {
			t.Fatalf("create %s: %v", code, err)
		}
		return e
	}
	owner := mk("GT000", "Owner", "super_admin", 1000, nil)
	hr := mk("GT010", "Hr Admin", "hr_admin", 1010, nil)
	mgr := mk("GT020", "Mona Manager", "manager", 1020, nil)
	dara := mk("GT001", "Dara Sok", "staff", 1001, &mgr.ID)
	sokha := mk("GT002", "Sokha Chan", "staff", 1002, nil)
	// The test simulates October 2026; make the employees pre-date it.
	if _, err := rawDB.Exec("UPDATE employees SET created_at = '2026-01-01'"); err != nil {
		t.Fatal(err)
	}
	const daraTG, sokhaTG, mgrTG, hrTG, ownerTG = 1001, 1002, 1020, 1010, 1000

	// ---------- Telegram is configured in Settings (no environment variables) ----------
	if _, err := empSvc.CreateUser(ctx, cli, owner.ID, "owner@example.com", "owner-password-123"); err != nil {
		t.Fatal(err)
	}
	ownerJWT, _ := h.login("owner@example.com", "owner-password-123")
	if code, body, _ := h.app("GET", "/me", daraTG, nil); code != 503 || errCode(body) != "TELEGRAM_NOT_CONFIGURED" {
		t.Fatalf("before a bot token is set the Mini App must say it isn't configured: %d %v", code, body)
	}
	if code, body, _ := h.admin("PUT", "/settings", ownerJWT, map[string]any{"telegram_bot_token": "987654321:AAHBadBadBadBadBadBadBadBadBadBadBad"}); code != 400 || errCode(body) != "TELEGRAM_TOKEN_REJECTED" {
		t.Fatalf("a token Telegram rejects must not be saved: %d %v", code, body)
	}
	if code, body, _ := h.admin("PUT", "/settings", ownerJWT, map[string]any{"telegram_bot_token": "not a token"}); code != 400 {
		t.Fatalf("malformed token: %d %v", code, body)
	}
	tgSettings := map[string]any{"telegram_bot_token": botToken, "miniapp_url": appURL, "telegram_group_id": -1001234567890, "telegram_review_chat_id": 0}
	code, body, raw := h.admin("PUT", "/settings", ownerJWT, tgSettings)
	saved, _ := body["settings"].(map[string]any)
	if code != 200 || saved["telegram_bot_token_set"] != true || saved["telegram_bot_username"] != "att_test_bot" || saved["miniapp_url"] != appURL {
		t.Fatalf("save Telegram settings: %d %v", code, body)
	}
	var groupID string
	h.db.QueryRow("SELECT value FROM company_settings WHERE `key` = 'telegram_group_id'").Scan(&groupID)
	if groupID != "-1001234567890" {
		t.Fatalf("chat IDs must be stored exactly, got %q", groupID)
	}
	if strings.Contains(string(raw), botToken) {
		t.Fatal("the bot token must never be returned")
	}
	var storedToken, auditRows string
	h.db.QueryRow("SELECT value FROM company_settings WHERE `key` = 'telegram_bot_token'").Scan(&storedToken)
	if !strings.HasPrefix(storedToken, "enc:v1:") || strings.Contains(storedToken, "AAHdq") {
		t.Fatalf("the bot token must be encrypted at rest: %q", storedToken)
	}
	h.db.QueryRow("SELECT GROUP_CONCAT(details) FROM audit_logs WHERE action = 'settings.update'").Scan(&auditRows)
	if strings.Contains(auditRows, "AAHdq") || !strings.Contains(auditRows, "(changed)") {
		t.Fatalf("the audit log must record that the token changed, not the token: %s", auditRows)
	}
	if len(ftg.calls("setChatMenuButton", 0)) == 0 {
		time.Sleep(300 * time.Millisecond) // the menu button is updated in the background
	}
	if mb := ftg.calls("setChatMenuButton", 0); len(mb) == 0 || !strings.Contains(markup(map[string]any{"reply_markup": mb[len(mb)-1]["menu_button"]}), appURL) {
		t.Fatalf("saving the Mini App URL must point the bot's menu button at it: %v", mb)
	}

	// ---------- Mini App authentication ----------
	if w, _ := h.do("GET", "/api/v1/app/me", nil); w.Code != 401 {
		t.Fatalf("app without initData must be 401, got %d", w.Code)
	}
	if w, body := h.do("GET", "/api/v1/app/me", nil, withHeader("Authorization", "tma "+initData("999:wrong-token", sokhaTG, time.Now()))); w.Code != 401 || errCode(body) != "INVALID_INIT_DATA" {
		t.Fatalf("forged initData must be 401: %d %v", w.Code, body)
	}
	if w, body := h.do("GET", "/api/v1/app/me", nil, withHeader("Authorization", "tma "+initData(botToken, sokhaTG, time.Now().Add(-25*time.Hour)))); w.Code != 401 || errCode(body) != "INIT_DATA_EXPIRED" {
		t.Fatalf("expired initData must be 401: %d %v", w.Code, body)
	}
	if code, body, _ := h.app("GET", "/me", 9999, nil); code != 403 || errCode(body) != "NOT_REGISTERED" {
		t.Fatalf("unregistered Mini App user: %d %v", code, body)
	}
	code, body, _ = h.app("GET", "/me", daraTG, nil)
	if me, _ := body["me"].(map[string]any); code != 200 || me["employee_code"] != "GT001" || me["is_reviewer"] != false || me["branch_name"] != "Main branch" {
		t.Fatalf("app me: %d %v", code, body)
	}

	// ---------- empty lists are [] (never null): clients index into them ----------
	isEmptyList := func(v any) bool { l, ok := v.([]any); return ok && len(l) == 0 }
	for _, path := range []string{"/attendance/pending", "/leave/pending", "/leave/mine"} {
		if code, body, _ := h.app("GET", path, hrTG, nil); code != 200 || !isEmptyList(body["items"]) {
			t.Fatalf("%s with nothing in it must return items: [] — got %d %v", path, code, body)
		}
	}

	// ---------- check-in (Thu 08 Oct 2026, 09:15 => late 15) ----------
	h.set("2026-10-08 09:15")
	code, body, _ = h.app("GET", "/attendance/today", daraTG, nil)
	if ci, _ := body["check_in"].(map[string]any); code != 200 || body["work_day"] != true || body["attendance"] != nil || ci["allowed"] != true {
		t.Fatalf("today before check-in: %d %v", code, body)
	}
	if code, body, _ := h.app("POST", "/attendance/check-in", daraTG, map[string]string{}); code != 400 || errCode(body) != "PHOTO_REQUIRED" {
		t.Fatalf("check-in without photo: %d %v", code, body)
	}
	if code, body, _ := h.app("POST", "/attendance/check-in", daraTG, map[string]string{"photo": "not an image"}); code != 400 || errCode(body) != "INVALID_PHOTO" {
		t.Fatalf("non-image upload: %d %v", code, body)
	}
	if code, body, _ := h.app("POST", "/attendance/check-in", daraTG, map[string]string{"photo": photo("x"), "latitude": "abc", "longitude": "104.9"}); code != 400 || errCode(body) != "INVALID_LOCATION" {
		t.Fatalf("bad location: %d %v", code, body)
	}
	if code, body, _ := h.app("POST", "/attendance/check-in", daraTG, map[string]string{"photo": photo("big") + strings.Repeat("x", attendance.MaxUploadBytes)}); code != 413 {
		t.Fatalf("oversized upload must be 413: %d %v", code, body)
	}
	inPhoto := photo("dara-in")
	code, body, _ = h.app("POST", "/attendance/check-in", daraTG, map[string]string{"photo": inPhoto, "latitude": "11.5564", "longitude": "104.9282", "accuracy": "12"})
	if code != 200 || body["pending"] != true || num(body, "attendance", "late_minutes") != 15 || body["location_label"] != "Received" {
		t.Fatalf("check-in: %d %v", code, body)
	}
	attID := uint64(num(body, "attendance", "id"))
	if len(store.objs) != 1 {
		t.Fatalf("photo not stored: %d objects", len(store.objs))
	}
	// Dara's manager reviews her (her branch has no branch admin yet), so only the
	// manager is notified — not every HR/super admin. The photo is uploaded and the
	// message opens the Mini App's review page.
	photos := ftg.calls("sendPhoto", 0)
	if len(photos) != 1 || photos[0]["chat_id"] != float64(mgrTG) || photos[0]["_upload"] != true {
		t.Fatalf("reviewer photo notifications: %v", photos)
	}
	if !strings.Contains(markup(photos[0]), `"web_app":{"url":"`+appURL+`team"}`) {
		t.Fatalf("review notification must open the Mini App: %s", markup(photos[0]))
	}
	var fileID, digest string
	h.db.QueryRow("SELECT telegram_file_id, content_sha256 FROM attendance_photos WHERE attendance_id = ?", attID).Scan(&fileID, &digest)
	if fileID != "tg-uploaded" || len(digest) != 64 {
		t.Fatalf("photo row: file_id=%q sha=%q", fileID, digest)
	}
	var channel string
	h.db.QueryRow("SELECT channel FROM audit_logs WHERE action='attendance.check_in' AND entity_id = ?", attID).Scan(&channel)
	if channel != auth.ChannelMiniApp {
		t.Fatalf("check-in audited with channel %q", channel)
	}
	if code, body, _ := h.app("POST", "/attendance/check-in", daraTG, map[string]string{"photo": photo("dara-in2")}); code != 409 {
		t.Fatalf("double check-in must be 409: %d %v", code, body)
	}
	h.set("2026-10-08 09:30")
	if code, body, _ := h.app("POST", "/attendance/check-out", sokhaTG, map[string]string{"photo": photo("s1")}); code != 409 {
		t.Fatalf("check-out without check-in must fail: %d %v", code, body)
	}
	code, body, _ = h.app("GET", "/attendance/today", daraTG, nil)
	if ci, co := body["check_in"].(map[string]any), body["check_out"].(map[string]any); code != 200 || body["attendance"] == nil || ci["allowed"] != false || co["allowed"] != true {
		t.Fatalf("today after check-in: %d %v", code, body)
	}

	// ---------- review: manager scope ----------
	var confID uint64
	h.db.QueryRow("SELECT id FROM attendance_confirmations WHERE attendance_id = ? AND type='CHECK_IN'", attID).Scan(&confID)
	if code, _, _ := h.app("GET", "/attendance/pending", sokhaTG, nil); code != 403 {
		t.Fatal("staff must not list pending reviews")
	}
	code, body, _ = h.app("GET", "/attendance/pending", mgrTG, nil)
	items, _ := body["items"].([]any)
	if code != 200 || len(items) != 1 || num(items[0].(map[string]any), "photo_id") == 0 {
		t.Fatalf("manager pending (team only, with photo_id): %d %v", code, body)
	}
	photoPath := fmt.Sprintf("/attendance/photos/%d", uint64(num(items[0].(map[string]any), "photo_id")))
	if code, _, raw := h.app("GET", photoPath, mgrTG, nil); code != 200 || string(raw) != inPhoto {
		t.Fatalf("manager photo stream: %d", code)
	}
	if code, _, _ := h.app("GET", photoPath, daraTG, nil); code != 200 {
		t.Fatal("employees can see their own photo")
	}
	if code, _, _ := h.app("GET", photoPath, sokhaTG, nil); code != 404 {
		t.Fatal("staff must not see other people's photos")
	}
	code, body, _ = h.app("GET", fmt.Sprintf("/attendance/%d", attID), mgrTG, nil)
	if ph, _ := body["photos"].([]any); code != 200 || len(ph) != 1 || ph[0].(map[string]any)["url"] != "/api/v1/app"+photoPath {
		t.Fatalf("attendance detail photo URLs must use the app prefix: %d %v", code, body)
	}
	if code, body, _ := h.app("POST", fmt.Sprintf("/attendance/confirmations/%d/confirm", confID), sokhaTG, nil); code != 403 {
		t.Fatalf("staff must not confirm: %d %v", code, body)
	}
	code, body, _ = h.app("POST", fmt.Sprintf("/attendance/confirmations/%d/confirm", confID), mgrTG, nil)
	if code != 200 || body["reviewer"].(map[string]any)["full_name"] != "Mona Manager" {
		t.Fatalf("manager confirm: %d %v", code, body)
	}
	if code, body, _ := h.app("POST", fmt.Sprintf("/attendance/confirmations/%d/confirm", confID), hrTG, nil); code != 409 || errCode(body) != "ALREADY_REVIEWED" {
		t.Fatalf("second review must be 409: %d %v", code, body)
	}

	// ---------- check-out, reject, resubmit ----------
	h.set("2026-10-08 17:32")
	if code, body, _ := h.app("POST", "/attendance/check-out", daraTG, map[string]string{"photo": inPhoto}); code != 400 || errCode(body) != "PHOTO_REUSED" {
		t.Fatalf("re-used photo must be rejected: %d %v", code, body)
	}
	code, body, _ = h.app("POST", "/attendance/check-out", daraTG, map[string]string{"photo": photo("dara-out1")})
	if code != 200 || num(body, "attendance", "working_minutes") != 497 {
		t.Fatalf("check-out (8h 17m): %d %v", code, body)
	}
	var outConf uint64
	h.db.QueryRow("SELECT id FROM attendance_confirmations WHERE attendance_id = ? AND type='CHECK_OUT'", attID).Scan(&outConf)
	if code, body, _ := h.app("POST", fmt.Sprintf("/attendance/confirmations/%d/reject", outConf), hrTG, nil); code != 400 || errCode(body) != "REASON_REQUIRED" {
		t.Fatalf("reject without reason: %d %v", code, body)
	}
	if code, body, _ := h.app("POST", fmt.Sprintf("/attendance/confirmations/%d/reject", outConf), hrTG,
		map[string]any{"reason": "Please provide a valid attendance photo."}); code != 200 {
		t.Fatalf("reject: %d %v", code, body)
	}
	var cs string
	h.db.QueryRow("SELECT confirmation_status FROM attendance WHERE id = ?", attID).Scan(&cs)
	if cs != models.ConfRejected {
		t.Fatalf("attendance should be REJECTED, got %s", cs)
	}
	msgs := ftg.calls("sendMessage", daraTG)
	if len(msgs) == 0 || !strings.Contains(fmt.Sprint(msgs[len(msgs)-1]["text"]), "Open the app") || !strings.Contains(markup(msgs[len(msgs)-1]), `"web_app"`) {
		t.Fatalf("employee must be told (with an app button) about the rejection: %v", msgs)
	}
	h.set("2026-10-08 17:40")
	if code, body, _ := h.app("POST", "/attendance/check-out", daraTG, map[string]string{"photo": photo("dara-out2")}); code != 200 || body["resubmission"] != true {
		t.Fatalf("re-submit check-out: %d %v", code, body)
	}
	if code, body, _ := h.app("POST", "/attendance/check-out", daraTG, map[string]string{"photo": photo("dara-out3")}); code != 409 {
		t.Fatalf("double check-out must be 409: %d %v", code, body)
	}
	var working int
	h.db.QueryRow("SELECT working_minutes, confirmation_status FROM attendance WHERE id = ?", attID).Scan(&working, &cs)
	if working != 505 || cs != models.ConfPending {
		t.Fatalf("after resubmit: working=%d conf=%s", working, cs)
	}

	// ---------- reports & permissions ----------
	if code, _, _ := h.app("GET", "/reports/daily", daraTG, nil); code != 403 {
		t.Fatal("staff must not read all-staff reports")
	}
	names := func(body map[string]any) string {
		var out []string
		for _, r := range body["rows"].([]any) {
			out = append(out, fmt.Sprint(r.(map[string]any)["full_name"]))
		}
		return strings.Join(out, ",")
	}
	code, body, _ = h.app("GET", "/reports/daily?date=2026-10-08", hrTG, nil)
	if code != 200 || !strings.Contains(names(body), "Dara Sok") || !strings.Contains(names(body), "Sokha Chan") {
		t.Fatalf("HR daily report: %d %v", code, body)
	}
	code, body, _ = h.app("GET", "/reports/daily?date=2026-10-08", mgrTG, nil)
	if code != 200 || !strings.Contains(names(body), "Dara Sok") || strings.Contains(names(body), "Sokha Chan") {
		t.Fatalf("manager report must only include the team (and self): %v", names(body))
	}
	code, body, _ = h.app("GET", "/attendance/history?month=2026-10", daraTG, nil)
	if sum, _ := body["summary"].(map[string]any); code != 200 || sum == nil || sum["present_days"] != float64(1) {
		t.Fatalf("my history: %d %v", code, body)
	}
	for _, r := range body["rows"].([]any) {
		if r.(map[string]any)["employee_code"] != "GT001" {
			t.Fatalf("history must only contain the caller's rows: %v", r)
		}
	}

	// ---------- leave ----------
	h.set("2026-10-08 10:00")
	preview := map[string]any{"leave_type_id": 1, "start_date": "12-10-2026", "end_date": "13-10-2026"} // no reason yet
	if code, body, _ := h.app("POST", "/leave/preview", sokhaTG, preview); code != 200 || body["days"] != float64(2) {
		t.Fatalf("leave preview: %d %v", code, body)
	}
	if code, body, _ := h.app("POST", "/leave", sokhaTG, preview); code != 400 {
		t.Fatalf("submitting without a reason must fail: %d %v", code, body)
	}
	leaveReq := map[string]any{"leave_type_id": 1, "start_date": "12-10-2026", "end_date": "13-10-2026", "reason": "Personal matter"}
	code, body, _ = h.app("POST", "/leave", sokhaTG, leaveReq)
	if code != 201 || body["days"] != float64(2) || body["status"] != models.LeavePending {
		t.Fatalf("leave submit: %d %v", code, body)
	}
	leaveID := uint64(num(body, "id"))
	if m := ftg.calls("sendMessage", hrTG); len(m) == 0 || !strings.Contains(markup(m[len(m)-1]), appURL+"team") {
		t.Fatalf("reviewers must get a leave notification that opens the app: %v", m)
	}
	if code, body, _ := h.app("POST", "/leave", sokhaTG, leaveReq); code != 409 || errCode(body) != "OVERLAP" {
		t.Fatalf("overlap must be rejected: %d %v", code, body)
	}
	if code, _, _ := h.app("POST", fmt.Sprintf("/leave/%d/approve", leaveID), mgrTG, nil); code != 403 {
		t.Fatal("manager must not approve leave outside the team")
	}
	if code, body, _ := h.app("POST", fmt.Sprintf("/leave/%d/approve", leaveID), hrTG, nil); code != 200 || body["status"] != models.LeaveApproved {
		t.Fatalf("approve leave: %d %v", code, body)
	}
	var leaveRows int
	h.db.QueryRow("SELECT COUNT(*) FROM attendance WHERE status='LEAVE' AND work_date IN ('2026-10-12','2026-10-13')").Scan(&leaveRows)
	if leaveRows != 2 {
		t.Fatalf("approved leave should create 2 LEAVE days, got %d", leaveRows)
	}
	h.set("2026-10-12 08:50")
	if code, body, _ := h.app("POST", "/attendance/check-in", sokhaTG, map[string]string{"photo": photo("s2")}); code != 409 {
		t.Fatalf("check-in on leave day must be blocked: %d %v", code, body)
	}
	h.set("2026-10-08 10:00")
	lv := map[string]any{"leave_type_id": 1, "start_date": "2026-10-20", "end_date": "2026-10-21", "reason": "Family event"}
	code, body, _ = h.app("POST", "/leave", sokhaTG, lv)
	if code != 201 {
		t.Fatalf("second leave: %d %v", code, body)
	}
	secondLeave := uint64(num(body, "id"))
	if code, body, _ := h.app("GET", "/leave/mine", sokhaTG, nil); code != 200 || len(body["items"].([]any)) != 2 {
		t.Fatalf("my leave: %d %v", code, body)
	}
	if code, _, _ := h.app("POST", fmt.Sprintf("/leave/%d/cancel", secondLeave), hrTG, nil); code == 200 {
		t.Fatal("only the owner may cancel a leave request")
	}
	if code, body, _ := h.app("POST", fmt.Sprintf("/leave/%d/cancel", secondLeave), sokhaTG, nil); code != 200 || body["status"] != models.LeaveCancelled {
		t.Fatalf("cancel leave: %d %v", code, body)
	}

	// ---------- weekend is never absent; day close marks absent ----------
	h.set("2026-10-08 23:55")
	sch := scheduler.New(gdb, rdb, st, repSvc, notifier)
	sch.Now = now
	if err := sch.CloseDay(ctx, time.Date(2026, 10, 8, 0, 0, 0, 0, time.UTC), nil); err != nil {
		t.Fatal(err)
	}
	var absent int
	h.db.QueryRow("SELECT COUNT(*) FROM attendance WHERE status='ABSENT' AND work_date='2026-10-08'").Scan(&absent)
	if absent != 4 { // owner, hr, manager, sokha — dara checked in
		t.Fatalf("expected 4 absent on 08 Oct, got %d", absent)
	}
	h.set("2026-10-10 12:00") // Saturday
	if err := sch.CloseDay(ctx, time.Date(2026, 10, 10, 0, 0, 0, 0, time.UTC), nil); err != nil {
		t.Fatal(err)
	}
	h.db.QueryRow("SELECT COUNT(*) FROM attendance WHERE work_date='2026-10-10'").Scan(&absent)
	if absent != 0 {
		t.Fatalf("weekend must not create absences, got %d", absent)
	}
	if err := sch.DailyReport(ctx, time.Date(2026, 10, 8, 0, 0, 0, 0, time.UTC), nil); err != nil || len(ftg.calls("sendMessage", -1001234567890)) == 0 {
		t.Fatalf("daily report not posted to group: %v", err)
	}

	// ---------- admin panel: login, session cookie, CSRF ----------
	if _, err := empSvc.CreateUser(ctx, cli, hr.ID, "hr@example.com", "correct-horse-battery"); err != nil {
		t.Fatal(err)
	}
	if w, _ := h.do("POST", "/api/v1/auth/login", map[string]any{"email": "hr@example.com", "password": "wrong-password"}); w.Code != 401 {
		t.Fatalf("bad password must be 401: %d", w.Code)
	}
	jwt, session := h.login("hr@example.com", "correct-horse-battery")
	if session == nil || !session.HttpOnly || session.SameSite != http.SameSiteStrictMode || session.Path != "/api/" || session.MaxAge <= 0 {
		t.Fatalf("login must set an HttpOnly SameSite=Strict session cookie: %+v", session)
	}
	if w, body := h.do("GET", "/api/v1/auth/me", nil, withCookie(session)); w.Code != 200 || body["role"] != "hr_admin" {
		t.Fatalf("cookie session: %d %v", w.Code, body)
	}
	sokhaPath := fmt.Sprintf("/api/v1/employees/%d", sokha.ID)
	if w, body := h.do("PUT", sokhaPath, map[string]any{"position": "QA"}, withCookie(session)); w.Code != 403 {
		t.Fatalf("cookie-authenticated write without X-Requested-With must be refused (CSRF): %d %v", w.Code, body)
	}
	if w, body := h.do("PUT", sokhaPath, map[string]any{"position": "QA"}, withCookie(session), withHeader("X-Requested-With", "fetch")); w.Code != 200 || body["position"] != "QA" {
		t.Fatalf("cookie write with X-Requested-With: %d %v", w.Code, body)
	}
	if code, body, _ := h.app("GET", "/me", 0, nil); code == 200 {
		t.Fatalf("Mini App routes must not accept anything but initData: %v", body)
	}
	if w, _ := h.do("GET", "/api/v1/app/me", nil, withHeader("Authorization", "Bearer "+jwt)); w.Code != 401 {
		t.Fatalf("a panel JWT must not open the Mini App API: %d", w.Code)
	}
	if w, _ := h.do("GET", "/api/v1/auth/me", nil, withHeader("Authorization", "tma "+initData(botToken, hrTG, time.Now()))); w.Code != 401 {
		t.Fatalf("Mini App initData must not open the admin API: %d", w.Code)
	}

	// ---------- admin panel: manual edit, audit, photos, export ----------
	code, body, _ = h.admin("PUT", fmt.Sprintf("/attendance/%d", attID), jwt, map[string]any{"check_in": "09:00", "check_out": "17:30", "reason": "Camera outage"})
	if code != 200 || body["status"] != models.StatusPresent || body["confirmation_status"] != models.ConfConfirmed {
		t.Fatalf("manual edit: %d %v", code, body)
	}
	var auditN int
	h.db.QueryRow("SELECT COUNT(*) FROM audit_logs WHERE action='attendance.manual_edit'").Scan(&auditN)
	if auditN != 1 {
		t.Fatalf("manual edit not audited")
	}
	if _, err := h.db.Exec("UPDATE audit_logs SET action='x'"); err == nil {
		t.Fatal("audit_logs must be append-only")
	}
	if _, err := h.db.Exec("DELETE FROM audit_logs"); err == nil {
		t.Fatal("audit_logs must not be deletable")
	}
	if code, _, _ := h.admin("GET", "/audit-logs", jwt, nil); code != 403 {
		t.Fatal("HR must not read audit logs (super admin only)")
	}
	var photoID uint64
	h.db.QueryRow("SELECT id FROM attendance_photos WHERE attendance_id = ? ORDER BY id LIMIT 1", attID).Scan(&photoID)
	if w, _ := h.do("GET", fmt.Sprintf("/api/v1/attendance/photos/%d", photoID), nil, withCookie(session)); w.Code != 200 || w.Body.String() != inPhoto {
		t.Fatalf("photo stream with session cookie: %d", w.Code)
	}
	code, body, _ = h.admin("GET", "/reports/summary?employee_id="+fmt.Sprint(dara.ID)+"&from=01-10-2026&to=08-10-2026", jwt, nil)
	if sums, _ := body["summaries"].([]any); code != 200 || len(sums) != 1 || sums[0].(map[string]any)["employee_code"] != "GT001" {
		t.Fatalf("staff range report: %d %v", code, body)
	}
	if code, body, _ := h.admin("GET", "/reports/summary?department_id=999999&from=2026-10-01&to=2026-10-08", jwt, nil); code != 200 || !isEmptyList(body["rows"]) || !isEmptyList(body["summaries"]) {
		t.Fatalf("a report matching nobody must return rows/summaries: [] — got %d %v", code, body)
	}
	for _, path := range []string{"/holidays?year=1999", "/leave?status=REJECTED", "/attendance?from=2020-01-01&to=2020-01-02"} {
		if code, body, _ := h.admin("GET", path, jwt, nil); code != 200 || !isEmptyList(body["items"]) {
			t.Fatalf("%s with nothing in it must return items: [] — got %d %v", path, code, body)
		}
	}
	if w, _ := h.do("GET", "/api/v1/reports/export?format=xlsx&from=2026-10-01&to=2026-10-31", nil, withCookie(session)); w.Code != 200 || !bytes.HasPrefix(w.Body.Bytes(), []byte("PK")) {
		t.Fatalf("xlsx export: %d", w.Code)
	}
	w, _ := h.do("POST", "/api/v1/auth/logout", nil, withCookie(session), withHeader("X-Requested-With", "fetch"))
	if cleared := w.Result().Cookies(); w.Code != 204 || len(cleared) != 1 || cleared[0].MaxAge >= 0 {
		t.Fatalf("logout must clear the session cookie: %d %+v", w.Code, cleared)
	}

	// super admin: panel login for an employee, audit log access
	if code, body, _ := h.admin("POST", fmt.Sprintf("/employees/%d/user", mgr.ID), jwt, map[string]any{"email": "mona@example.com", "password": "manager-pass-123"}); code != 403 {
		t.Fatalf("only a super admin may create logins: %d %v", code, body)
	}
	if code, body, _ := h.admin("POST", fmt.Sprintf("/employees/%d/user", mgr.ID), ownerJWT, map[string]any{"email": "mona@example.com", "password": "manager-pass-123"}); code != 201 {
		t.Fatalf("super admin creates a login: %d %v", code, body)
	}
	if code, body, _ := h.admin("GET", "/audit-logs?limit=5", ownerJWT, nil); code != 200 || len(body["items"].([]any)) != 5 {
		t.Fatalf("super admin reads audit logs: %d %v", code, body)
	}
	_ = ownerTG

	// ---------- branches: the owner assigns one admin per branch ----------
	const vuthyTG, bophaTG = 1030, 1040
	if code, _, _ := h.admin("POST", "/branches", jwt, map[string]any{"code": "SR", "name": "Siem Reap"}); code != 403 {
		t.Fatal("only the owner creates branches (HR cannot)")
	}
	code, body, _ = h.admin("POST", "/branches", ownerJWT, map[string]any{"code": "sr", "name": "Siem Reap", "address": "Sivatha Blvd"})
	if code != 201 || body["code"] != "SR" {
		t.Fatalf("create branch: %d %v", code, body)
	}
	srID := uint64(num(body, "id"))
	if code, body, _ := h.admin("POST", "/branches", ownerJWT, map[string]any{"code": "SR", "name": "Dup"}); code != 409 {
		t.Fatalf("duplicate branch code: %d %v", code, body)
	}
	mkAPI := func(jwtTok string, in map[string]any) (int, map[string]any) {
		code, body, _ := h.admin("POST", "/employees", jwtTok, in)
		return code, body
	}
	if code, body := mkAPI(jwt, map[string]any{"employee_code": "GT030", "full_name": "Vuthy Ly", "telegram_user_id": vuthyTG}); code != 400 || errCode(body) != "BRANCH_REQUIRED" {
		t.Fatalf("staff need a branch: %d %v", code, body)
	}
	code, body = mkAPI(jwt, map[string]any{"employee_code": "GT030", "full_name": "Vuthy Ly", "telegram_user_id": vuthyTG, "branch_id": srID, "manager_id": mgr.ID})
	if code != 201 {
		t.Fatalf("create SR staff: %d %v", code, body)
	}
	vuthyID := uint64(num(body, "id"))
	code, body = mkAPI(jwt, map[string]any{"employee_code": "GT040", "full_name": "Bopha Keo", "telegram_user_id": bophaTG, "branch_id": srID})
	if code != 201 {
		t.Fatalf("create SR staff 2: %d %v", code, body)
	}
	bophaID := uint64(num(body, "id"))
	if code, body := mkAPI(jwt, map[string]any{"employee_code": "GT041", "full_name": "X", "role": "branch_admin", "branch_id": srID}); code != 400 || errCode(body) != "BRANCH_ADMIN_ROLE" {
		t.Fatalf("branch_admin is only granted via the branch: %d %v", code, body)
	}
	assign := func(jwtTok string, branchID uint64, empID any) (int, map[string]any) {
		code, body, _ := h.admin("PUT", fmt.Sprintf("/branches/%d/admin", branchID), jwtTok, map[string]any{"employee_id": empID})
		return code, body
	}
	if code, _ := assign(jwt, srID, bophaID); code != 403 {
		t.Fatal("HR cannot assign branch admins — only the owner")
	}
	if code, body := assign(ownerJWT, srID, owner.ID); code != 400 {
		t.Fatalf("the owner cannot be a branch admin: %d %v", code, body)
	}
	if code, body := assign(ownerJWT, srID, bophaID); code != 200 || num(body, "admin_employee_id") != float64(bophaID) {
		t.Fatalf("assign branch admin: %d %v", code, body)
	}
	if code, body := assign(ownerJWT, mainBranch, bophaID); code != 409 || errCode(body) != "ALREADY_BRANCH_ADMIN" {
		t.Fatalf("one person admins one branch: %d %v", code, body)
	}
	var bophaRole uint8
	h.db.QueryRow("SELECT role_id FROM employees WHERE id = ?", bophaID).Scan(&bophaRole)
	if bophaRole != models.RoleBranchAdmin {
		t.Fatalf("assigned admin gets the branch_admin role, got %d", bophaRole)
	}
	if code, body, _ := h.admin("POST", fmt.Sprintf("/employees/%d/user", bophaID), ownerJWT, map[string]any{"email": "bopha@example.com", "password": "bopha-password-123"}); code != 201 {
		t.Fatalf("panel login for branch admin: %d %v", code, body)
	}
	bophaJWT, _ := h.login("bopha@example.com", "bopha-password-123")

	// the branch admin sees and manages only their branch
	code, body, _ = h.admin("GET", "/employees", bophaJWT, nil)
	var codes []string
	for _, e := range body["items"].([]any) {
		codes = append(codes, fmt.Sprint(e.(map[string]any)["employee_code"]))
	}
	if code != 200 || strings.Join(codes, ",") != "GT030,GT040" {
		t.Fatalf("branch admin's employee list must be their branch only: %d %v", code, codes)
	}
	if code, _, _ := h.admin("GET", fmt.Sprintf("/employees/%d", sokha.ID), bophaJWT, nil); code != 404 {
		t.Fatal("other branches' staff are invisible to a branch admin")
	}
	if code, _, _ := h.admin("PUT", fmt.Sprintf("/employees/%d", sokha.ID), bophaJWT, map[string]any{"position": "x"}); code != 404 {
		t.Fatal("branch admin cannot edit another branch's staff")
	}
	if code, body, _ := h.admin("PUT", fmt.Sprintf("/employees/%d", vuthyID), bophaJWT, map[string]any{"position": "Cashier"}); code != 200 || body["position"] != "Cashier" {
		t.Fatalf("branch admin edits their own staff: %d %v", code, body)
	}
	if code, body := mkAPI(bophaJWT, map[string]any{"employee_code": "GT042", "full_name": "Srey Mom", "branch_id": mainBranch}); code != 403 {
		t.Fatalf("branch admin cannot add staff to another branch: %d %v", code, body)
	}
	if code, body := mkAPI(bophaJWT, map[string]any{"employee_code": "GT043", "full_name": "Dina Ros", "role": "hr_admin"}); code != 403 {
		t.Fatalf("branch admin cannot grant admin roles: %d %v", code, body)
	}
	code, body = mkAPI(bophaJWT, map[string]any{"employee_code": "GT042", "full_name": "Srey Mom", "telegram_user_id": 1042})
	if code != 201 || num(body, "branch_id") != float64(srID) {
		t.Fatalf("branch admin's new staff land in their branch: %d %v", code, body)
	}
	if code, body, _ := h.admin("GET", "/branches", bophaJWT, nil); code != 200 || len(body["items"].([]any)) != 1 {
		t.Fatalf("branch admin lists only their branch: %d %v", code, body)
	}
	if code, _, _ := h.admin("POST", "/branches", bophaJWT, map[string]any{"code": "X1", "name": "X"}); code != 403 {
		t.Fatal("branch admins cannot create branches")
	}
	if code, _, _ := h.admin("PUT", fmt.Sprintf("/branches/%d", srID), bophaJWT, map[string]any{"name": "Renamed"}); code != 403 {
		t.Fatal("branch admins cannot rename their branch")
	}
	if code, _, _ := h.admin("PUT", fmt.Sprintf("/branches/%d", mainBranch), bophaJWT, map[string]any{"telegram_group_id": -5}); code != 403 {
		t.Fatal("branch admins cannot change another branch")
	}
	srOffice := map[string]any{"office_latitude": 13.3633, "office_longitude": 103.8564, "allowed_radius_meters": 200, "telegram_group_id": -200}
	if code, body, _ := h.admin("PUT", fmt.Sprintf("/branches/%d", srID), bophaJWT, srOffice); code != 200 || num(body, "telegram_group_id") != -200 {
		t.Fatalf("branch admin sets their branch's office and group: %d %v", code, body)
	}
	if code, _, _ := h.admin("GET", "/settings", bophaJWT, nil); code != 403 {
		t.Fatal("company settings are not for branch admins")
	}

	// a check-in in Siem Reap: the branch's office location applies; the branch admin
	// (and Vuthy's manager) are notified; the first DM uploads, the second reuses the file_id
	h.set("2026-10-14 08:55") // Wednesday
	if _, err := st.Update(ctx, owner.ID, map[string]string{"gps_enabled": "true", "gps_reject_outside": "true"}); err != nil {
		t.Fatal(err)
	}
	before := len(ftg.calls("sendPhoto", 0))
	if code, body, _ := h.app("POST", "/attendance/check-in", vuthyTG, map[string]string{"photo": photo("vuthy-pp"), "latitude": "11.5564", "longitude": "104.9282"}); code != 400 || errCode(body) != "OUTSIDE_OFFICE" {
		t.Fatalf("a Siem Reap employee in Phnom Penh is outside their branch office: %d %v", code, body)
	}
	code, body, _ = h.app("POST", "/attendance/check-in", vuthyTG, map[string]string{"photo": photo("vuthy-in"), "latitude": "13.3634", "longitude": "103.8565"})
	if code != 200 || !strings.HasPrefix(fmt.Sprint(body["location_label"]), "Inside office radius") {
		t.Fatalf("check-in at the branch office: %d %v", code, body)
	}
	if _, err := st.Update(ctx, owner.ID, map[string]string{"gps_enabled": "false", "gps_reject_outside": "false"}); err != nil {
		t.Fatal(err)
	}
	sent := ftg.calls("sendPhoto", 0)[before:]
	chats := map[float64]bool{}
	for _, p := range sent {
		chats[p["chat_id"].(float64)] = true
	}
	if len(sent) != 2 || !chats[bophaTG] || !chats[mgrTG] || chats[hrTG] || chats[ownerTG] {
		t.Fatalf("Siem Reap reviews go to the branch admin and the manager only: %v", sent)
	}
	if sent[0]["_upload"] != true || sent[1]["photo"] != "tg-uploaded" {
		t.Fatalf("the second reviewer must get the saved file_id: %v", sent)
	}
	code, body, _ = h.admin("GET", "/attendance/pending", bophaJWT, nil)
	pend, _ := body["items"].([]any)
	if code != 200 || len(pend) != 1 || pend[0].(map[string]any)["employee_code"] != "GT030" {
		t.Fatalf("branch admin's review queue = their branch: %d %v", code, body)
	}
	vuthyConf := uint64(num(pend[0].(map[string]any), "confirmation_id"))
	if code, body, _ := h.admin("POST", fmt.Sprintf("/attendance/confirmations/%d/confirm", vuthyConf), bophaJWT, nil); code != 200 {
		t.Fatalf("branch admin confirms: %d %v", code, body)
	}
	code, body, _ = h.admin("GET", "/reports/summary?from=2026-10-01&to=2026-10-14", bophaJWT, nil)
	for _, sum := range body["summaries"].([]any) {
		if sm := sum.(map[string]any); sm["branch"] != "Siem Reap" {
			t.Fatalf("branch admin's reports cover only their branch: %v", sm)
		}
	}
	if code != 200 || len(body["summaries"].([]any)) != 3 {
		t.Fatalf("branch report: %d %v", code, body)
	}
	code, body, _ = h.admin("GET", fmt.Sprintf("/reports/daily?date=2026-10-14&branch_id=%d", srID), ownerJWT, nil)
	if code != 200 || len(body["rows"].([]any)) != 3 {
		t.Fatalf("owner filters the daily report by branch: %d %v", code, body)
	}
	if code, body, _ := h.app("GET", "/attendance/pending", bophaTG, nil); code != 200 || len(body["items"].([]any)) != 0 {
		t.Fatalf("branch admin's Mini App review tab works too: %d %v", code, body)
	}
	if err := sch.DailyReport(ctx, time.Date(2026, 10, 14, 0, 0, 0, 0, time.UTC), nil); err != nil {
		t.Fatal(err)
	}
	if m := ftg.calls("sendMessage", -200); len(m) == 0 || !strings.Contains(fmt.Sprint(m[len(m)-1]["text"]), "SIEM REAP") ||
		strings.Contains(fmt.Sprint(m[len(m)-1]["text"]), "Dara Sok") {
		t.Fatalf("each branch's group gets only that branch's daily report: %v", m)
	}

	// replacing the admin demotes the previous one; deactivating an admin frees the branch
	if code, body := assign(ownerJWT, srID, vuthyID); code != 200 || num(body, "admin_employee_id") != float64(vuthyID) {
		t.Fatalf("reassign: %d %v", code, body)
	}
	h.db.QueryRow("SELECT role_id FROM employees WHERE id = ?", bophaID).Scan(&bophaRole)
	if bophaRole != models.RoleStaff {
		t.Fatalf("the previous branch admin becomes staff, got role %d", bophaRole)
	}
	if code, _, _ := h.admin("GET", "/employees", bophaJWT, nil); code != 401 {
		t.Fatalf("a demoted admin's panel session ends immediately, got %d", code)
	}
	if err := empSvc.Deactivate(ctx, &auth.Actor{Employee: *owner, Channel: auth.ChannelDashboard}, vuthyID); err != nil {
		t.Fatal(err)
	}
	var adminLeft sql.NullInt64
	h.db.QueryRow("SELECT admin_employee_id FROM branches WHERE id = ?", srID).Scan(&adminLeft)
	if adminLeft.Valid {
		t.Fatal("deactivating a branch admin must leave the branch without an admin")
	}

	// ---------- deactivation keeps history and blocks the Mini App ----------
	if err := empSvc.Deactivate(ctx, &auth.Actor{Employee: *hr, Channel: auth.ChannelDashboard}, dara.ID); err != nil {
		t.Fatal(err)
	}
	if code, body, _ := h.app("GET", "/me", daraTG, nil); code != 403 {
		t.Fatalf("deactivated employee must be blocked: %d %v", code, body)
	}
	var hist int
	h.db.QueryRow("SELECT COUNT(*) FROM attendance WHERE employee_id = ?", dara.ID).Scan(&hist)
	if hist == 0 {
		t.Fatal("history must survive deactivation")
	}
	if w, body := h.do("GET", "/health", nil); w.Code != 200 || body["database"] != "connected" {
		t.Fatalf("health: %d %v", w.Code, body)
	}
}

func envOr(k, def string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return def
}
