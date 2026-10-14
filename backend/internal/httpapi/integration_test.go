package httpapi

// Integration test: real MySQL + Redis, fake Telegram, in-memory photo storage.
// Drives the two real clients' APIs: the Telegram Mini App (/api/v1/app, signed
// initData) and the HR admin panel (/api/v1, session cookie or Bearer JWT).
// Runs only when TEST_DATABASE_URL is set (a MySQL user allowed to create databases):
//
//	TEST_DATABASE_URL='root:pass@tcp(127.0.0.1:3306)/' TEST_REDIS_URL='redis://127.0.0.1:6379/15' go test ./internal/httpapi -run Integration -v

import (
	"attendance/backend/internal/bots"
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

	"attendance/backend/internal/access"
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
	"attendance/backend/internal/schedule"
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
func (m *memStore) Delete(_ context.Context, key string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.objs, key)
	return nil
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
		body["_token"] = strings.TrimPrefix(strings.Split(strings.TrimPrefix(r.URL.Path, "/"), "/")[0], "bot")
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

// admin calls the admin panel API with a Bearer JWT. Lists come whole (page_size=0)
// unless the path asks for a page itself; TestPaging covers the default.
func (h *harness) admin(method, path, jwt string, body any) (int, map[string]any, []byte) {
	h.t.Helper()
	if method == "GET" && !strings.Contains(path, "page") {
		sep := "?"
		if strings.Contains(path, "?") {
			sep = "&"
		}
		path += sep + "page_size=0"
	}
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

func errCodeOf(v any) string {
	m, _ := v.(map[string]any)
	s, _ := m["code"].(string)
	return s
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
	botReg := bots.New(gdb, box)
	tgc := telegram.New(func(context.Context) string { return "" }, ftg.srv.URL)
	notifier := notification.New(gdb, tgc, botReg).Sync()
	store := &memStore{objs: map[string][]byte{}}
	authSvc := auth.NewService(gdb, conf.JWTSecret, conf.JWTTTL)
	empSvc := employee.NewService(gdb)
	attSvc := attendance.NewService(gdb, st, store, notifier)
	attSvc.Now = now
	leaveSvc := leave.NewService(gdb, st, notifier)
	leaveSvc.Now = now
	repSvc := report.NewService(gdb, st)
	repSvc.Now = now
	schedSvc := schedule.NewService(gdb, st)
	schedSvc.Now = now
	srv := New(conf, Deps{DB: gdb, Redis: rdb, Store: store, Auth: authSvc, Settings: st, Employees: empSvc,
		Branches: branch.NewService(gdb), Attendance: attSvc, Leave: leaveSvc, Reports: repSvc, Schedules: schedSvc, Access: access.NewService(gdb), Telegram: tgc, Bots: botReg})
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
	// The owner and HR are system users (admin accounts, not staff); their Telegram
	// links let them review in the Mini App and get notifications.
	accSvc := access.NewService(gdb)
	mkSys := func(name, email, pw, role string, tg int64) *models.Employee {
		u, err := accSvc.CreateUser(ctx, cli, access.UserInput{FullName: &name, Email: &email, Password: &pw, Role: &role, TelegramUserID: &tg})
		if err != nil {
			t.Fatalf("system user %s: %v", email, err)
		}
		var e models.Employee
		if err := gdb.First(&e, u.PersonID).Error; err != nil || e.Kind != models.KindSystem {
			t.Fatalf("system user %s person: %v %+v", email, err, e)
		}
		return &e
	}
	owner := mkSys("Owner", "owner@example.com", "owner-password-123", "super_admin", 1000)
	hr := mkSys("Hr Admin", "hr@example.com", "correct-horse-battery", "hr_admin", 1010)
	mgr := mk("GT020", "Mona Manager", "manager", 1020, nil)
	dara := mk("GT001", "Dara Sok", "staff", 1001, &mgr.ID)
	sokha := mk("GT002", "Sokha Chan", "staff", 1002, nil)
	// The test simulates October 2026; make the employees pre-date it.
	if _, err := rawDB.Exec("UPDATE employees SET created_at = '2026-01-01'"); err != nil {
		t.Fatal(err)
	}
	const daraTG, sokhaTG, mgrTG, hrTG, ownerTG = 1001, 1002, 1020, 1010, 1000

	// ---------- Telegram: each branch has its own bot, set on the branch page ----------
	ownerJWT, _ := h.login("owner@example.com", "owner-password-123")
	if code, body, _ := h.app("GET", "/me", daraTG, nil); code != 503 || errCode(body) != "TELEGRAM_NOT_CONFIGURED" {
		t.Fatalf("before any branch has a bot the Mini App must say it isn't configured: %d %v", code, body)
	}
	mainPath := fmt.Sprintf("/branches/%d", mainBranch)
	if code, body, _ := h.admin("PUT", mainPath, ownerJWT, map[string]any{"telegram_bot_token": "987654321:AAHBadBadBadBadBadBadBadBadBadBadBad"}); code != 400 || errCode(body) != "TELEGRAM_TOKEN_REJECTED" {
		t.Fatalf("a token Telegram rejects must not be saved: %d %v", code, body)
	}
	if code, body, _ := h.admin("PUT", mainPath, ownerJWT, map[string]any{"telegram_bot_token": "not a token"}); code != 400 || errCode(body) != "INVALID_BOT_TOKEN" {
		t.Fatalf("malformed token: %d %v", code, body)
	}
	if code, body, _ := h.admin("PUT", "/settings", ownerJWT, map[string]any{"telegram_bot_token": botToken}); code != 400 || errCode(body) != "UNKNOWN_SETTING" {
		t.Fatalf("Telegram is not a company setting any more: %d %v", code, body)
	}
	tgSettings := map[string]any{"telegram_bot_token": botToken, "miniapp_url": appURL, "telegram_group_id": -1001234567890, "telegram_review_chat_id": 0}
	code, body, raw := h.admin("PUT", mainPath, ownerJWT, tgSettings)
	if code != 200 || body["telegram_bot_set"] != true || body["telegram_bot_username"] != "att_test_bot" || body["miniapp_url"] != appURL {
		t.Fatalf("save the branch's Telegram: %d %v", code, body)
	}
	var groupID int64
	h.db.QueryRow("SELECT telegram_group_id FROM branches WHERE id = ?", mainBranch).Scan(&groupID)
	if groupID != -1001234567890 {
		t.Fatalf("chat IDs must be stored exactly, got %d", groupID)
	}
	if strings.Contains(string(raw), botToken) {
		t.Fatal("the bot token must never be returned")
	}
	var storedToken, auditRows string
	h.db.QueryRow("SELECT telegram_bot_token FROM branches WHERE id = ?", mainBranch).Scan(&storedToken)
	if !strings.HasPrefix(storedToken, "enc:v1:") || strings.Contains(storedToken, "AAHdq") {
		t.Fatalf("the bot token must be encrypted at rest: %q", storedToken)
	}
	h.db.QueryRow("SELECT GROUP_CONCAT(details) FROM audit_logs WHERE action = 'branch.update'").Scan(&auditRows)
	if strings.Contains(auditRows, "AAHdq") || !strings.Contains(auditRows, `"telegram_bot_set": true`) {
		t.Fatalf("the audit log must record that the bot changed, not the token: %s", auditRows)
	}
	if len(ftg.calls("setChatMenuButton", 0)) == 0 {
		time.Sleep(300 * time.Millisecond) // the menu button is updated in the background
	}
	if mb := ftg.calls("setChatMenuButton", 0); len(mb) == 0 || !strings.Contains(markup(map[string]any{"reply_markup": mb[len(mb)-1]["menu_button"]}), appURL) {
		t.Fatalf("saving the Mini App URL must point the bot's menu button at it: %v", mb)
	}
	var annualID, personalID uint64 // the Main branch's own leave types
	h.db.QueryRow("SELECT id FROM leave_types WHERE branch_id = ? AND code = 'ANNUAL'", mainBranch).Scan(&annualID)
	h.db.QueryRow("SELECT id FROM leave_types WHERE branch_id = ? AND code = 'PERSONAL'", mainBranch).Scan(&personalID)
	if annualID == 0 || personalID == 0 {
		t.Fatal("migration 0008 must give the Main branch its own leave types")
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
	if ci, _ := body["check_in"].(map[string]any); code != 200 || body["work_day"] != true || body["attendance"] != nil || ci["allowed"] != true ||
		ci["reason_required"] != true || ci["late_minutes"] != float64(15) {
		t.Fatalf("today before check-in (15 min late: a reason is needed): %d %v", code, body)
	}
	if code, body, _ := h.app("POST", "/attendance/check-in", daraTG, map[string]string{}); code != 400 || errCode(body) != "REASON_REQUIRED" ||
		!strings.Contains(fmt.Sprint(body), "15 min late") {
		t.Fatalf("a late check-in needs a reason: %d %v", code, body)
	}
	if code, body, _ := h.app("POST", "/attendance/check-in", daraTG, map[string]string{"reason": strings.Repeat("x", 256)}); code != 400 || errCode(body) != "REASON_TOO_LONG" {
		t.Fatalf("reasons are at most 255 characters: %d %v", code, body)
	}
	if code, body, _ := h.app("POST", "/attendance/check-in", daraTG, map[string]string{"photo": "not an image", "reason": "Traffic jam"}); code != 400 || errCode(body) != "INVALID_PHOTO" {
		t.Fatalf("non-image upload: %d %v", code, body)
	}
	if code, body, _ := h.app("POST", "/attendance/check-in", daraTG, map[string]string{"photo": photo("x"), "latitude": "abc", "longitude": "104.9"}); code != 400 || errCode(body) != "INVALID_LOCATION" {
		t.Fatalf("bad location: %d %v", code, body)
	}
	if code, body, _ := h.app("POST", "/attendance/check-in", daraTG, map[string]string{"photo": photo("big") + strings.Repeat("x", attendance.MaxUploadBytes)}); code != 413 {
		t.Fatalf("oversized upload must be 413: %d %v", code, body)
	}
	inPhoto := photo("dara-in")
	code, body, _ = h.app("POST", "/attendance/check-in", daraTG, map[string]string{"photo": inPhoto, "latitude": "11.5564", "longitude": "104.9282", "accuracy": "12", "reason": "Traffic jam"})
	if code != 200 || body["pending"] != true || num(body, "attendance", "late_minutes") != 15 || body["location_label"] != "Received" ||
		body["attendance"].(map[string]any)["check_in_reason"] != "Traffic jam" {
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
	if code != 200 || len(items) != 1 || num(items[0].(map[string]any), "photo_id") == 0 || items[0].(map[string]any)["reason"] != "Traffic jam" {
		t.Fatalf("manager pending (team only, with photo_id and the late reason): %d %v", code, body)
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
	if sc, _ := body["schedule"].(map[string]any); sc == nil || sc["work"] != true || sc["start"] != "09:00" || sc["end"] != "17:30" {
		t.Fatalf("attendance detail must include the day's schedule: %v", body["schedule"])
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
	// The Mini App sends no photo: the reviewer gets a text message instead.
	sentPhotos := len(ftg.calls("sendPhoto", 0))
	if code, body, _ := h.app("POST", "/attendance/check-out", daraTG, map[string]string{"latitude": "11.5564", "longitude": "104.9282"}); code != 200 || body["resubmission"] != true {
		t.Fatalf("re-submit check-out without a photo: %d %v", code, body)
	}
	if n := len(ftg.calls("sendPhoto", 0)); n != sentPhotos {
		t.Fatalf("no photo, so no sendPhoto: %d -> %d", sentPhotos, n)
	}
	if m := ftg.calls("sendMessage", mgrTG); len(m) == 0 || !strings.Contains(fmt.Sprint(m[len(m)-1]["text"]), "Check-Out") || strings.Contains(fmt.Sprint(m[len(m)-1]["text"]), "Photo") {
		t.Fatalf("reviewer must get a text notification for a photo-less check-out: %v", m)
	}
	code, body, _ = h.app("GET", "/attendance/pending", mgrTG, nil)
	if items, _ := body["items"].([]any); code != 200 || len(items) != 1 || items[0].(map[string]any)["photo_id"] != nil || items[0].(map[string]any)["type"] != "CHECK_OUT" {
		t.Fatalf("pending check-out without a photo must have photo_id null: %d %v", code, body)
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
	preview := map[string]any{"leave_type_id": annualID, "start_date": "12-10-2026", "end_date": "13-10-2026"} // no reason yet
	if code, body, _ := h.app("POST", "/leave/preview", sokhaTG, preview); code != 200 || body["days"] != float64(2) {
		t.Fatalf("leave preview: %d %v", code, body)
	}
	if code, body, _ := h.app("POST", "/leave", sokhaTG, preview); code != 400 {
		t.Fatalf("submitting without a reason must fail: %d %v", code, body)
	}
	leaveReq := map[string]any{"leave_type_id": annualID, "start_date": "12-10-2026", "end_date": "13-10-2026", "reason": "Personal matter"}
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
	lv := map[string]any{"leave_type_id": annualID, "start_date": "2026-10-20", "end_date": "2026-10-21", "reason": "Family event"}
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

	// ---------- half-day leave: the halves come from that day's shift (09:00-17:30) ----------
	half := map[string]any{"leave_type_id": annualID, "start_date": "2026-10-22", "end_date": "2026-10-22", "day_type": "FIRST_HALF"}
	code, body, _ = h.app("POST", "/leave/preview", sokhaTG, half)
	plan, _ := body["plan"].(map[string]any)
	if code != 200 || body["days"] != 0.5 || body["problem"] != nil || plan["work_type"] != "NORMAL" ||
		fmt.Sprint(plan["first_half"]) != "[09:00 13:15]" || fmt.Sprint(plan["second_half"]) != "[13:15 17:30]" {
		t.Fatalf("half-day preview: %d %v", code, body)
	}
	code, body, _ = h.app("POST", "/leave/preview", sokhaTG, map[string]any{"leave_type_id": annualID, "start_date": "2026-10-22", "end_date": "2026-10-23", "day_type": "FIRST_HALF"})
	if p, _ := body["problem"].(map[string]any); code != 200 || p["code"] != "HALF_DAY_ONE_DATE" || body["plan"] == nil {
		t.Fatalf("a half day spanning two dates must come back as a problem next to the plan: %d %v", code, body)
	}
	if code, body, _ := h.app("POST", "/leave/preview", sokhaTG, map[string]any{"leave_type_id": annualID, "start_date": "2026-10-24", "end_date": "2026-10-24", "day_type": "SECOND_HALF"}); code != 200 || errCodeOf(body["problem"]) != "NO_WORKING_DAYS" {
		t.Fatalf("a half day on a Saturday: %d %v", code, body)
	}
	half["reason"] = "Dentist"
	code, body, _ = h.app("POST", "/leave", sokhaTG, half)
	if code != 201 || body["days"] != 0.5 || body["day_type"] != "FIRST_HALF" || body["half_from"] != "09:00" || body["half_to"] != "13:15" {
		t.Fatalf("half-day submit: %d %v", code, body)
	}
	halfID := uint64(num(body, "id"))
	if code, body, _ := h.app("POST", "/leave", sokhaTG, half); code != 409 || errCode(body) != "OVERLAP" {
		t.Fatalf("the same half twice must be refused: %d %v", code, body)
	}
	if code, body, _ := h.app("POST", "/leave", sokhaTG, map[string]any{"leave_type_id": annualID, "start_date": "2026-10-22", "end_date": "2026-10-22", "reason": "x"}); code != 409 || errCode(body) != "OVERLAP" {
		t.Fatalf("a full day over a half day must be refused: %d %v", code, body)
	}
	code, body, _ = h.app("POST", "/leave", sokhaTG, map[string]any{"leave_type_id": annualID, "start_date": "2026-10-22", "end_date": "2026-10-22", "day_type": "SECOND_HALF", "reason": "Rest"})
	if code != 201 || body["half_from"] != "13:15" {
		t.Fatalf("the other half of the same date is allowed: %d %v", code, body)
	}
	if code, body, _ := h.app("POST", fmt.Sprintf("/leave/%d/cancel", uint64(num(body, "id"))), sokhaTG, nil); code != 200 {
		t.Fatalf("cancel second half: %d %v", code, body)
	}
	if code, body, _ := h.app("POST", fmt.Sprintf("/leave/%d/approve", halfID), hrTG, nil); code != 200 || body["status"] != models.LeaveApproved {
		t.Fatalf("approve half day: %d %v", code, body)
	}
	h.db.QueryRow("SELECT COUNT(*) FROM attendance WHERE status='LEAVE' AND work_date='2026-10-22'").Scan(&leaveRows)
	if leaveRows != 0 {
		t.Fatal("a half day must not mark the whole day as leave")
	}
	// Morning off: she starts at 13:15 — 13:20 is within grace, 13:40 is 25 min late.
	h.set("2026-10-22 13:20")
	code, body, _ = h.app("GET", "/attendance/today", sokhaTG, nil)
	if hl, _ := body["half_leave"].(map[string]any); code != 200 || hl["day_type"] != "FIRST_HALF" || body["schedule_start"] != "13:15" || body["check_in"].(map[string]any)["allowed"] != true {
		t.Fatalf("today with a morning off: %d %v", code, body)
	}
	if code, body, _ := h.app("POST", "/attendance/check-in", sokhaTG, map[string]string{}); code != 200 || num(body, "attendance", "late_minutes") != 0 {
		t.Fatalf("check-in after a morning off (no photo, on time): %d %v", code, body)
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
	if absent != 2 { // manager, sokha — dara checked in; the owner and HR are not staff
		t.Fatalf("expected 2 absent on 08 Oct, got %d", absent)
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
	// ---------- one special schedule for several people ----------
	batch := map[string]any{"employee_ids": []uint64{dara.ID, sokha.ID}, "start_date": "2026-11-02", "end_date": "2026-11-06", "kind": "OFF", "reason": "Training"}
	code, body, _ = h.admin("POST", "/schedule-overrides", jwt, batch)
	if items, _ := body["items"].([]any); code != 201 || len(items) != 2 {
		t.Fatalf("special schedule for two people: %d %v", code, body)
	}
	batch["employee_ids"] = []uint64{sokha.ID, mgr.ID}
	if code, body, _ := h.admin("POST", "/schedule-overrides", jwt, batch); code != 409 || errCode(body) != "OVERLAP" || !strings.Contains(fmt.Sprint(body), "Sokha Chan") {
		t.Fatalf("a batch with someone already on a special schedule must be refused, naming them: %d %v", code, body)
	}
	var specials int
	h.db.QueryRow("SELECT COUNT(*) FROM schedule_overrides WHERE start_date = '2026-11-02'").Scan(&specials)
	if specials != 2 {
		t.Fatalf("a refused batch must create nothing: %d rows", specials)
	}
	h.db.Exec("DELETE FROM schedule_overrides WHERE start_date = '2026-11-02'")

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

	// ---------- system users are admin accounts, separate from employees ----------
	viewer := map[string]any{"full_name": "Mona Viewer", "email": "mona@example.com", "password": "manager-pass-123", "role": "viewer"}
	if code, body, _ := h.admin("POST", "/system-users", jwt, viewer); code != 403 {
		t.Fatalf("only the owner creates system users: %d %v", code, body)
	}
	if code, body, _ := h.admin("POST", "/roles", ownerJWT, map[string]any{"name": "Viewer", "permissions": []string{"all.view", "report.export"}}); code != 201 {
		t.Fatalf("read-only admin role: %d %v", code, body)
	}
	viewer["role"] = "manager"
	if code, body, _ := h.admin("POST", "/system-users", ownerJWT, viewer); code != 400 || errCode(body) != "STAFF_ROLE" {
		t.Fatalf("managers are employees (Mini App), not system users: %d %v", code, body)
	}
	viewer["role"] = "viewer"
	if code, body, _ := h.admin("POST", "/system-users", ownerJWT, viewer); code != 201 || body["role"] != "viewer" || body["scope"] != "all" {
		t.Fatalf("owner creates a system user: %d %v", code, body)
	}
	if code, body, _ := h.admin("POST", "/employees", ownerJWT, map[string]any{"employee_code": "GT011", "full_name": "X", "role": "hr_admin"}); code != 400 || errCode(body) != "ADMIN_ROLE" {
		t.Fatalf("employees cannot get admin roles: %d %v", code, body)
	}
	if _, body, _ := h.admin("GET", "/employees", ownerJWT, nil); strings.Contains(fmt.Sprint(body), "Mona Viewer") || strings.Contains(fmt.Sprint(body), "Hr Admin") {
		t.Fatalf("system users are not listed as employees: %v", body)
	}
	if code, _, _ := h.admin("GET", fmt.Sprintf("/employees/%d", hr.ID), ownerJWT, nil); code != 404 {
		t.Fatalf("a system user is not an employee record, got %d", code)
	}
	if code, body, _ := h.app("POST", "/attendance/check-in", hrTG, map[string]string{}); code != 403 {
		t.Fatalf("system users do not check in: %d %v", code, body)
	}
	if code, body, _ := h.app("GET", "/me", hrTG, nil); code != 200 || body["me"].(map[string]any)["is_system_user"] != true {
		t.Fatalf("the Mini App knows a system user: %d %v", code, body)
	}
	if code, body, _ := h.admin("GET", "/audit-logs?page=2&page_size=5", ownerJWT, nil); code != 200 || len(body["items"].([]any)) != 5 || num(body, "total") < 10 || num(body, "page") != 2 {
		t.Fatalf("super admin reads audit logs, a page at a time: %d %v", code, body["total"])
	}
	_ = ownerTG

	// ---------- branches: the owner assigns one admin per branch ----------
	const vuthyTG, bophaTG, bophaStaffTG = 1030, 1040, 1041 // Bopha: her admin account's Telegram, and her own as staff
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
	code, body = mkAPI(jwt, map[string]any{"employee_code": "GT040", "full_name": "Bopha Keo", "telegram_user_id": bophaStaffTG, "branch_id": srID})
	if code != 201 {
		t.Fatalf("create SR staff 2: %d %v", code, body)
	}
	bophaID := uint64(num(body, "id"))
	if code, body := mkAPI(jwt, map[string]any{"employee_code": "GT041", "full_name": "X", "role": "branch_admin", "branch_id": srID}); code != 400 || errCode(body) != "ADMIN_ROLE" {
		t.Fatalf("an employee cannot be a branch admin (that is a system user): %d %v", code, body)
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
	if code, body := assign(ownerJWT, srID, bophaID); code != 400 || errCode(body) != "INVALID_EMPLOYEE" {
		t.Fatalf("a branch admin is a system user, not an employee: %d %v", code, body)
	}
	// Bopha also runs the branch: her admin account is a separate system user, with
	// its own Telegram for reviewing in the Mini App.
	code, body, _ = h.admin("POST", "/system-users", ownerJWT, map[string]any{"full_name": "Bopha Keo", "email": "bopha@example.com",
		"password": "bopha-password-123", "role": "branch_admin", "branch_id": srID, "telegram_user_id": bophaTG})
	if code != 201 || num(body, "branch_id") != float64(srID) || num(body, "telegram_user_id") != bophaTG {
		t.Fatalf("branch admin system user: %d %v", code, body)
	}
	bophaAdmin := uint64(num(body, "person_id"))
	if _, body, _ := h.admin("GET", fmt.Sprintf("/branches/%d", srID), ownerJWT, nil); num(body, "admin_employee_id") != float64(bophaAdmin) {
		t.Fatalf("a Branch Admin system user takes the branch: %v", body)
	}
	if code, body, _ := h.admin("POST", "/system-users", ownerJWT, map[string]any{"full_name": "Y", "email": "y@example.com",
		"password": "y-password-1234", "role": "branch_admin", "branch_id": srID}); code != 409 || errCode(body) != "BRANCH_HAS_ADMIN" {
		t.Fatalf("one admin per branch: %d %v", code, body)
	}
	if code, body := assign(ownerJWT, mainBranch, bophaAdmin); code != 409 || errCode(body) != "ALREADY_BRANCH_ADMIN" {
		t.Fatalf("one person admins one branch: %d %v", code, body)
	}
	var bophaRole uint8
	h.db.QueryRow("SELECT role_id FROM employees WHERE id = ?", bophaAdmin).Scan(&bophaRole)
	if bophaRole != models.RoleBranchAdmin {
		t.Fatalf("the branch admin has the branch_admin role, got %d", bophaRole)
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
	if code, body := mkAPI(bophaJWT, map[string]any{"employee_code": "GT043", "full_name": "Dina Ros", "role": "hr_admin"}); code != 400 || errCode(body) != "ADMIN_ROLE" {
		t.Fatalf("staff never get admin roles: %d %v", code, body)
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

	// Siem Reap has its own bot: its branch admin sets it on their branch.
	const srBotToken = "555555555:AAHsrSiemReapSiemReapSiemReapSiemRe"
	if code, body, _ := h.admin("PUT", fmt.Sprintf("/branches/%d", srID), bophaJWT, map[string]any{"telegram_bot_token": srBotToken, "miniapp_url": appURL}); code != 200 || body["telegram_bot_set"] != true {
		t.Fatalf("a branch admin sets their branch's bot: %d %v", code, body)
	}

	// a check-in in Siem Reap: the branch's office location applies; the branch admin
	// (and Vuthy's manager) are notified; the first DM uploads, the second reuses the file_id
	h.set("2026-10-14 08:55") // Wednesday
	srPath := fmt.Sprintf("/branches/%d", srID)
	if code, body, _ := h.admin("PUT", srPath, ownerJWT, map[string]any{"gps": map[string]any{"enabled": true, "reject_outside": true}}); code != 200 || body["gps_reject_outside"] != true {
		t.Fatalf("Siem Reap GPS rules: %d %v", code, body)
	}
	before := len(ftg.calls("sendPhoto", 0))
	if code, body, _ := h.app("POST", "/attendance/check-in", vuthyTG, map[string]string{"photo": photo("vuthy-pp"), "latitude": "11.5564", "longitude": "104.9282"}); code != 400 || errCode(body) != "OUTSIDE_OFFICE" {
		t.Fatalf("a Siem Reap employee in Phnom Penh is outside their branch office: %d %v", code, body)
	}
	code, body, _ = h.app("POST", "/attendance/check-in", vuthyTG, map[string]string{"photo": photo("vuthy-in"), "latitude": "13.3634", "longitude": "103.8565"})
	if code != 200 || !strings.HasPrefix(fmt.Sprint(body["location_label"]), "Inside office radius") {
		t.Fatalf("check-in at the branch office: %d %v", code, body)
	}
	if code, _, _ := h.admin("PUT", srPath, ownerJWT, map[string]any{"gps": map[string]any{"enabled": false}}); code != 200 {
		t.Fatalf("GPS off again: %d", code)
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
	for _, m := range sent {
		if m["_token"] != srBotToken {
			t.Fatalf("Siem Reap's messages go through Siem Reap's own bot: %v", m["_token"])
		}
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

	// replacing the admin takes the previous one's panel powers; deleting an admin frees the branch
	code, body, _ = h.admin("POST", "/system-users", ownerJWT, map[string]any{"full_name": "Kosal Admin", "email": "kosal@example.com",
		"password": "kosal-password-12", "role": "hr_admin"})
	if code != 201 {
		t.Fatalf("second system user: %d %v", code, body)
	}
	kosalAdmin, kosalUser := uint64(num(body, "person_id")), uint64(num(body, "id"))
	if code, body := assign(ownerJWT, srID, kosalAdmin); code != 200 || num(body, "admin_employee_id") != float64(kosalAdmin) {
		t.Fatalf("reassign: %d %v", code, body)
	}
	h.db.QueryRow("SELECT role_id FROM employees WHERE id = ?", bophaAdmin).Scan(&bophaRole)
	if bophaRole != models.RoleStaff {
		t.Fatalf("the previous branch admin keeps the account without panel powers, got role %d", bophaRole)
	}
	if code, _, _ := h.admin("GET", "/employees", bophaJWT, nil); code != 401 {
		t.Fatalf("a demoted admin's panel session ends immediately, got %d", code)
	}
	if code, _, _ := h.admin("DELETE", fmt.Sprintf("/system-users/%d", kosalUser), ownerJWT, nil); code != 204 {
		t.Fatalf("delete system user: %d", code)
	}
	var adminLeft sql.NullInt64
	h.db.QueryRow("SELECT admin_employee_id FROM branches WHERE id = ?", srID).Scan(&adminLeft)
	if adminLeft.Valid {
		t.Fatal("deleting a branch admin must leave the branch without an admin")
	}
	if err := empSvc.Deactivate(ctx, &auth.Actor{Employee: *owner, Channel: auth.ChannelDashboard}, vuthyID); err != nil {
		t.Fatal(err)
	}

	// ---------- shifts: normal and cross-day (night) ----------
	gps := map[string]string{"latitude": "11.5564", "longitude": "104.9282"}
	withPhoto := func(tag string) map[string]string { // a reason too: these check-ins are often late on purpose
		m := map[string]string{"photo": photo(tag), "reason": "Test: " + tag}
		for k, v := range gps {
			m[k] = v
		}
		return m
	}
	shiftIn := func(name, start, end, wt string) map[string]any {
		return map[string]any{"branch_id": mainBranch, "name": name, "start_time": start, "end_time": end, "work_type": wt, "grace_minutes": 15,
			"work_days": []int{1, 2, 3, 4, 5, 6}, "color": "#4f46e5"}
	}
	if code, body, _ := h.admin("POST", "/work-schedules", ownerJWT, shiftIn("Bad night", "22:00", "06:00", "NORMAL")); code != 400 || errCode(body) != "INVALID_HOURS" {
		t.Fatalf("a normal shift cannot end before it starts: %d %v", code, body)
	}
	if code, body, _ := h.admin("POST", "/work-schedules", ownerJWT, shiftIn("Bad day", "08:00", "17:00", "CROSS_DAY")); code != 400 || errCode(body) != "INVALID_HOURS" {
		t.Fatalf("a cross-day shift must end the next day: %d %v", code, body)
	}
	code, body, _ = h.admin("POST", "/work-schedules", ownerJWT, shiftIn("Night", "22:00", "06:00", "CROSS_DAY"))
	if code != 201 || body["work_type"] != "CROSS_DAY" {
		t.Fatalf("create night shift: %d %v", code, body)
	}
	nightID := uint64(num(body, "id"))
	if code, body, _ := h.admin("POST", "/work-schedules", jwt, shiftIn("Night", "21:00", "05:00", "CROSS_DAY")); code != 409 || errCode(body) != "SHIFT_NAME_TAKEN" {
		t.Fatalf("shift names are unique: %d %v", code, body)
	}
	code, body, _ = h.admin("POST", "/work-schedules", jwt, shiftIn("Morning", "07:00", "15:00", "NORMAL")) // HR may define shifts too
	if code != 201 {
		t.Fatalf("HR creates a shift: %d %v", code, body)
	}
	morningID := uint64(num(body, "id"))
	mgrJWT, _ := h.login("mona@example.com", "manager-pass-123") // the read-only admin (Viewer) from above
	if code, _, _ := h.admin("POST", "/work-schedules", mgrJWT, shiftIn("Late", "12:00", "20:00", "NORMAL")); code != 403 {
		t.Fatalf("a read-only admin cannot define shifts, got %d", code)
	}
	if code, body, _ := h.admin("PUT", fmt.Sprintf("/employees/%d", sokha.ID), jwt, map[string]any{"work_schedule_id": nightID}); code != 200 {
		t.Fatalf("give Sokha the night shift: %d %v", code, body)
	}
	code, body, _ = h.admin("GET", "/work-schedules", mgrJWT, nil)
	if code != 200 || len(body["items"].([]any)) != 3 {
		t.Fatalf("list shifts: %d %v", code, body)
	}

	// ---------- a night shift belongs to the day it starts ----------
	h.set("2026-10-19 21:55") // Monday evening
	code, body, _ = h.app("POST", "/attendance/check-in", sokhaTG, withPhoto("night-in-1"))
	if code != 200 || num(body, "attendance", "late_minutes") != 0 || !strings.HasPrefix(fmt.Sprint(body["attendance"].(map[string]any)["work_date"]), "2026-10-19") {
		t.Fatalf("night check-in: %d %v", code, body)
	}
	h.set("2026-10-20 02:00") // after midnight: the Mini App still shows Monday's shift, ready to check out
	code, body, _ = h.app("GET", "/attendance/today", sokhaTG, nil)
	if co, _ := body["check_out"].(map[string]any); code != 200 || !strings.HasPrefix(fmt.Sprint(body["date"]), "2026-10-19") || body["cross_day"] != true ||
		body["shift_name"] != "Night" || co["allowed"] != true {
		t.Fatalf("today during the night shift: %d %v", code, body)
	}
	h.set("2026-10-20 06:05")
	code, body, _ = h.app("POST", "/attendance/check-out", sokhaTG, withPhoto("night-out-1"))
	att := body["attendance"].(map[string]any)
	if code != 200 || !strings.HasPrefix(fmt.Sprint(att["work_date"]), "2026-10-19") || num(body, "attendance", "working_minutes") != 490 ||
		num(body, "attendance", "early_leave_minutes") != 0 {
		t.Fatalf("next-morning check-out must close Monday's shift: %d %v", code, body)
	}
	var tueRows int
	h.db.QueryRow("SELECT COUNT(*) FROM attendance WHERE employee_id = ? AND work_date = '2026-10-20'", sokha.ID).Scan(&tueRows)
	if tueRows != 0 {
		t.Fatal("the check-out must not open a Tuesday record")
	}
	h.set("2026-10-21 00:30") // very late for Tuesday's 22:00 shift
	code, body, _ = h.app("POST", "/attendance/check-in", sokhaTG, withPhoto("night-in-2"))
	if code != 200 || !strings.HasPrefix(fmt.Sprint(body["attendance"].(map[string]any)["work_date"]), "2026-10-20") || num(body, "attendance", "late_minutes") != 150 {
		t.Fatalf("a check-in after midnight is late for last night's shift: %d %v", code, body)
	}
	h.set("2026-10-21 05:40")
	code, body, _ = h.app("POST", "/attendance/check-out", sokhaTG, withPhoto("night-out-2"))
	if code != 200 || num(body, "attendance", "early_leave_minutes") != 20 {
		t.Fatalf("leaving 20 min before 06:00 is early: %d %v", code, body)
	}
	code, body, _ = h.admin("GET", "/reports/daily?date=2026-10-19", ownerJWT, nil)
	found := false
	for _, r := range body["rows"].([]any) {
		row := r.(map[string]any)
		if row["employee_code"] == "GT002" {
			found = row["shift"] == "Night" && row["shift_start"] == "22:00" && row["cross_day"] == true && row["status"] == models.StatusPresent
		}
	}
	if code != 200 || !found {
		t.Fatalf("the daily report shows the night shift: %d %v", code, body)
	}
	// The 10:00 missing check-in alert must not list someone whose shift starts at 22:00.
	h.set("2026-10-22 10:00")
	stNow, _ := st.Get(ctx)
	sentBefore := len(ftg.calls("sendMessage", 0))
	if err := sch.MissingCheckinAlert(ctx, time.Date(2026, 10, 22, 0, 0, 0, 0, time.UTC), stNow); err != nil {
		t.Fatal(err)
	}
	for _, m := range ftg.calls("sendMessage", 0)[sentBefore:] {
		if strings.Contains(fmt.Sprint(m["text"]), "Sokha") {
			t.Fatalf("night-shift staff are not missing at 10:00: %v", m["text"])
		}
	}
	// A manual entry for a night shift: 22:00 -> 06:00 is the next morning.
	code, body, _ = h.admin("POST", "/attendance", jwt, map[string]any{"employee_id": sokha.ID, "date": "2026-10-21", "check_in": "22:00", "check_out": "06:00", "reason": "Phone battery died"})
	if code != 201 && code != 200 || num(body, "working_minutes") != 480 {
		t.Fatalf("manual night shift: %d %v", code, body)
	}

	// ---------- weekly roster ----------
	rosterSet := func(tok string, items ...map[string]any) (int, map[string]any) {
		code, body, _ := h.admin("PUT", "/roster", tok, map[string]any{"items": items})
		return code, body
	}
	if code, _ := rosterSet(mgrJWT, map[string]any{"employee_id": dara.ID, "date": "2026-10-24", "work_schedule_id": morningID}); code != 403 {
		t.Fatalf("managers cannot edit the roster, got %d", code)
	}
	if code, body := rosterSet(jwt, map[string]any{"employee_id": dara.ID, "date": "2026-10-24", "work_schedule_id": morningID, "off": true}); code != 400 || errCode(body) != "INVALID_ITEM" {
		t.Fatalf("a change sets one thing: %d %v", code, body)
	}
	if code, body := rosterSet(jwt,
		map[string]any{"employee_id": dara.ID, "date": "2026-10-24", "work_schedule_id": morningID}, // Saturday: morning shift
		map[string]any{"employee_id": dara.ID, "date": "2026-10-23", "off": true},                   // Friday: day off
	); code != 200 || num(body, "updated") != 2 {
		t.Fatalf("set roster: %d %v", code, body)
	}
	code, body, _ = h.admin("GET", "/roster?from=2026-10-19", jwt, nil)
	cellOf := func(body map[string]any, code string, day int) map[string]any {
		for _, r := range body["rows"].([]any) {
			if row := r.(map[string]any); row["employee_code"] == code {
				return row["cells"].([]any)[day].(map[string]any)
			}
		}
		return nil
	}
	if code != 200 || len(body["days"].([]any)) != 7 {
		t.Fatalf("roster week: %d %v", code, body)
	}
	if sat := cellOf(body, "GT001", 5); sat["work"] != true || sat["source"] != "ROSTER" || sat["shift_name"] != "Morning" {
		t.Fatalf("roster Saturday: %v", sat)
	}
	if fri := cellOf(body, "GT001", 4); fri["work"] != false || fri["source"] != "ROSTER" {
		t.Fatalf("roster day off: %v", fri)
	}
	if mon := cellOf(body, "GT002", 0); mon["cross_day"] != true || mon["source"] != "DEFAULT" {
		t.Fatalf("usual night shift on the roster: %v", mon)
	}
	h.set("2026-10-24 07:20") // Saturday: Dara is rostered on the morning shift
	if code, body, _ := h.app("GET", "/attendance/today", daraTG, nil); code != 200 || body["work_day"] != true || body["shift_name"] != "Morning" || body["schedule_start"] != "07:00" {
		t.Fatalf("Mini App follows the roster: %d %v", code, body)
	}
	code, body, _ = h.admin("POST", "/roster/copy", jwt, map[string]any{"from": "2026-10-19", "to": "2026-10-26"})
	if code != 200 || num(body, "copied") < 2 {
		t.Fatalf("copy week: %d %v", code, body)
	}
	if _, body, _ := h.admin("GET", "/roster?from=2026-10-26", jwt, nil); cellOf(body, "GT001", 5)["shift_name"] != "Morning" {
		t.Fatalf("copied Saturday: %v", cellOf(body, "GT001", 5))
	}
	w, _ = h.do("GET", "/api/v1/roster/export?from=2026-10-19&format=csv", nil, withHeader("Authorization", "Bearer "+jwt))
	if w.Code != 200 || !strings.Contains(w.Body.String(), "Night 22:00–06:00 (+1)") || !strings.Contains(w.Body.String(), "Sokha Chan") {
		t.Fatalf("roster csv: %d %s", w.Code, w.Body.String())
	}
	w, _ = h.do("GET", "/api/v1/roster/export?from=2026-10-19", nil, withHeader("Authorization", "Bearer "+jwt))
	if w.Code != 200 || !strings.Contains(w.Header().Get("Content-Type"), "spreadsheetml") || w.Body.Len() < 2000 {
		t.Fatalf("roster xlsx: %d %s", w.Code, w.Header().Get("Content-Type"))
	}

	// ---------- special / temporary schedules ----------
	special := map[string]any{"employee_id": dara.ID, "start_date": "2026-10-27", "end_date": "2026-10-29", "kind": "CUSTOM",
		"start_time": "13:00", "end_time": "21:00", "work_type": "NORMAL", "work_days": []int{1, 2, 3, 4, 5}, "reason": "Evening cover"}
	if code, _, _ := h.admin("POST", "/schedule-overrides", mgrJWT, special); code != 403 {
		t.Fatalf("managers cannot set special schedules, got %d", code)
	}
	code, body, _ = h.admin("POST", "/schedule-overrides", jwt, special)
	created, _ := body["items"].([]any)
	if code != 201 || len(created) != 1 || created[0].(map[string]any)["kind"] != "CUSTOM" {
		t.Fatalf("create special schedule: %d %v", code, body)
	}
	specialID := uint64(num(created[0].(map[string]any), "id"))
	overlap := map[string]any{"employee_id": dara.ID, "start_date": "2026-10-29", "end_date": "2026-10-30", "kind": "OFF", "reason": "x"}
	if code, body, _ := h.admin("POST", "/schedule-overrides", jwt, overlap); code != 409 || errCode(body) != "OVERLAP" {
		t.Fatalf("special schedules of one person cannot overlap: %d %v", code, body)
	}
	overlap["start_date"] = "2026-10-30"
	if code, body, _ := h.admin("POST", "/schedule-overrides", jwt, overlap); code != 201 {
		t.Fatalf("day off special schedule: %d %v", code, body)
	}
	h.set("2026-10-27 12:00")
	code, body, _ = h.app("GET", "/attendance/today", daraTG, nil)
	if code != 200 || body["schedule_start"] != "13:00" || body["special"] != "Evening cover" {
		t.Fatalf("Mini App shows the special schedule: %d %v", code, body)
	}
	h.set("2026-10-27 13:20")
	if code, body, _ := h.app("POST", "/attendance/check-in", daraTG, withPhoto("dara-special")); code != 200 || num(body, "attendance", "late_minutes") != 20 {
		t.Fatalf("lateness follows the special hours: %d %v", code, body)
	}
	if code, body, _ := h.app("POST", "/leave/preview", daraTG, map[string]any{"leave_type_id": annualID, "start_date": "2026-10-30", "end_date": "2026-10-30"}); code != 200 || errCodeOf(body["problem"]) != "NO_WORKING_DAYS" {
		t.Fatalf("a special day off is not a working day for leave: %d %v", code, body)
	}
	if code, body, _ := h.admin("GET", "/schedule-overrides?when=upcoming", jwt, nil); code != 200 || len(body["items"].([]any)) != 1 {
		t.Fatalf("upcoming special schedules: %d %v", code, body)
	}
	if code, _, _ := h.admin("DELETE", fmt.Sprintf("/schedule-overrides/%d", specialID), jwt, nil); code != 204 {
		t.Fatalf("delete special schedule: %d", code)
	}

	// ---------- shifts in use cannot be deleted ----------
	if code, body, _ := h.admin("DELETE", fmt.Sprintf("/work-schedules/%d", nightID), ownerJWT, nil); code != 409 || errCode(body) != "SHIFT_IN_USE" {
		t.Fatalf("delete a shift in use: %d %v", code, body)
	}
	var defaultID uint64
	h.db.QueryRow("SELECT id FROM work_schedules WHERE is_default = 1").Scan(&defaultID)
	if code, body, _ := h.admin("DELETE", fmt.Sprintf("/work-schedules/%d", defaultID), ownerJWT, nil); code != 409 || errCode(body) != "SHIFT_IS_DEFAULT" {
		t.Fatalf("delete the default shift: %d %v", code, body)
	}
	code, body, _ = h.admin("POST", "/work-schedules", ownerJWT, shiftIn("Temp", "10:00", "14:00", "NORMAL"))
	if code != 201 {
		t.Fatal(body)
	}
	if code, _, _ := h.admin("DELETE", fmt.Sprintf("/work-schedules/%d", uint64(num(body, "id"))), ownerJWT, nil); code != 204 {
		t.Fatalf("delete an unused shift: %d", code)
	}
	var schedAudit int
	h.db.QueryRow("SELECT COUNT(*) FROM audit_logs WHERE action IN ('shift.create','roster.update','roster.copy','schedule_override.create','schedule_override.delete','shift.delete')").Scan(&schedAudit)
	if schedAudit < 9 {
		t.Fatalf("schedule changes are audited, got %d entries", schedAudit)
	}

	// ---------- role management ----------
	code, body, _ = h.admin("GET", "/roles/permissions", ownerJWT, nil)
	if code != 200 || len(body["items"].([]any)) < 20 {
		t.Fatalf("permission catalog: %d %v", code, body)
	}
	if code, _, _ := h.admin("GET", "/roles/permissions", jwt, nil); code != 403 {
		t.Fatalf("HR cannot manage roles, got %d", code)
	}
	regional := map[string]any{"name": "Regional Supervisor", "description": "Oversees a few branches",
		"permissions": []string{"attendance.self", "leave.self", "team.view", "team.review", "all.view", "all.review", "attendance.edit", "report.export"}}
	code, body, _ = h.admin("POST", "/roles", ownerJWT, regional)
	if code != 201 || body["code"] != "regional_supervisor" || num(body, "id") < 6 {
		t.Fatalf("create role: %d %v", code, body)
	}
	regionalID := int(num(body, "id"))
	if code, body, _ := h.admin("POST", "/roles", ownerJWT, map[string]any{"name": "Bad", "permissions": []string{"everything"}}); code != 400 || errCode(body) != "INVALID_PERMISSION" {
		t.Fatalf("unknown permission: %d %v", code, body)
	}
	if code, body, _ := h.admin("PUT", "/roles/1", ownerJWT, map[string]any{"permissions": []string{"attendance.self"}}); code != 400 || errCode(body) != "ROLE_LOCKED" {
		t.Fatalf("the owner role keeps every permission: %d %v", code, body)
	}
	if code, body, _ := h.admin("DELETE", "/roles/3", ownerJWT, nil); code != 409 || errCode(body) != "ROLE_SYSTEM" {
		t.Fatalf("built-in roles cannot be deleted: %d %v", code, body)
	}
	code, body, _ = h.admin("GET", "/roles", jwt, nil)
	if code != 200 || len(body["items"].([]any)) != 8 { // 5 built-in + Company Admin + Viewer + Regional Supervisor
		t.Fatalf("roles list: %d %v", code, body)
	}

	// ---------- system users & branch assignment ----------
	code, body, _ = h.admin("GET", "/system-users", ownerJWT, nil)
	if code != 200 || len(body["items"].([]any)) < 3 {
		t.Fatalf("system users: %d %v", code, body)
	}
	if code, body, _ := h.admin("POST", "/system-users", ownerJWT, map[string]any{"email": "sokha@example.com", "password": "sokha-password-123"}); code != 400 || errCode(body) != "USER_INCOMPLETE" {
		t.Fatalf("a system user needs a name and a role: %d %v", code, body)
	}
	code, body, _ = h.admin("POST", "/system-users", ownerJWT, map[string]any{"full_name": "Sokha Regional", "email": "sokha@example.com", "password": "sokha-password-123", "role": "regional_supervisor"})
	if code != 201 || body["role"] != "regional_supervisor" || body["scope"] != "all" {
		t.Fatalf("create system user with a role: %d %v", code, body)
	}
	sokhaUser := uint64(num(body, "id"))
	sokhaJWT, _ := h.login("sokha@example.com", "sokha-password-123")
	visible := func(tok string) []string {
		_, body, _ := h.admin("GET", "/employees", tok, nil)
		var codes []string
		for _, e := range body["items"].([]any) {
			codes = append(codes, fmt.Sprint(e.(map[string]any)["employee_code"]))
		}
		sort.Strings(codes)
		return codes
	}
	if v := visible(sokhaJWT); len(v) < 5 {
		t.Fatalf("no assigned branches = every branch: %v", v)
	}
	code, body, _ = h.admin("PUT", fmt.Sprintf("/system-users/%d/branches", sokhaUser), ownerJWT, map[string]any{"branch_ids": []uint64{srID}})
	if code != 200 || body["scope"] != "branches" {
		t.Fatalf("assign a branch: %d %v", code, body)
	}
	if v := strings.Join(visible(sokhaJWT), ","); !strings.Contains(v, "GT040") || strings.Contains(v, "GT001") || strings.Contains(v, "GT010") {
		t.Fatalf("a user limited to Siem Reap sees Siem Reap staff only (not Main branch, not HR): %v", v)
	}
	if code, _, _ := h.admin("GET", fmt.Sprintf("/employees/%d", dara.ID), sokhaJWT, nil); code != 404 && code != 403 {
		t.Fatalf("someone in another branch is out of reach, got %d", code)
	}
	if code, body, _ := h.admin("GET", "/branches", sokhaJWT, nil); code != 200 || len(body["items"].([]any)) != 1 {
		t.Fatalf("branch list follows the assignment: %d %v", code, body)
	}
	if code, body, _ := h.admin("POST", fmt.Sprintf("/system-users/%d/password", sokhaUser), ownerJWT, map[string]any{"password": "short"}); code != 400 || errCode(body) != "WEAK_PASSWORD" {
		t.Fatalf("weak password: %d %v", code, body)
	}
	if code, _, _ := h.admin("POST", fmt.Sprintf("/system-users/%d/password", sokhaUser), ownerJWT, map[string]any{"password": "brand-new-password-1"}); code != 204 {
		t.Fatalf("reset password: %d", code)
	}
	h.login("sokha@example.com", "brand-new-password-1")
	if code, _, _ := h.admin("PUT", fmt.Sprintf("/system-users/%d", sokhaUser), ownerJWT, map[string]any{"status": "INACTIVE"}); code != 200 {
		t.Fatalf("disable login: %d", code)
	}
	if w, _ := h.do("POST", "/api/v1/auth/login", map[string]any{"email": "sokha@example.com", "password": "brand-new-password-1"}); w.Code != 401 {
		t.Fatalf("a disabled login cannot sign in, got %d", w.Code)
	}
	var ownerUser uint64
	h.db.QueryRow("SELECT id FROM users WHERE employee_id = ?", owner.ID).Scan(&ownerUser)
	if code, body, _ := h.admin("PUT", fmt.Sprintf("/system-users/%d", ownerUser), ownerJWT, map[string]any{"status": "INACTIVE"}); code != 400 || errCode(body) != "SELF_DISABLE" {
		t.Fatalf("cannot disable yourself: %d %v", code, body)
	}
	if code, body, _ := h.admin("DELETE", fmt.Sprintf("/roles/%d", regionalID), ownerJWT, nil); code != 409 || errCode(body) != "ROLE_IN_USE" {
		t.Fatalf("a role in use cannot be deleted: %d %v", code, body)
	}
	if code, _, _ := h.admin("DELETE", fmt.Sprintf("/system-users/%d", sokhaUser), ownerJWT, nil); code != 204 {
		t.Fatalf("delete login: %d", code)
	}

	// ---------- departments & positions ----------
	code, body, _ = h.admin("POST", "/positions", jwt, map[string]any{"name": "Driver", "branch_id": mainBranch})
	if code != 201 {
		t.Fatalf("create position: %d %v", code, body)
	}
	driverID := uint64(num(body, "id"))
	if code, body, _ := h.admin("POST", "/positions", jwt, map[string]any{"name": "Driver", "branch_id": mainBranch}); code != 409 || errCode(body) != "NAME_TAKEN" {
		t.Fatalf("duplicate position: %d %v", code, body)
	}
	if code, _, _ := h.admin("POST", "/positions", mgrJWT, map[string]any{"name": "Cook", "branch_id": mainBranch}); code != 403 {
		t.Fatalf("managers cannot edit the lists, got %d", code)
	}
	if code, body, _ := h.admin("PUT", fmt.Sprintf("/employees/%d", dara.ID), jwt, map[string]any{"position_id": driverID}); code != 200 || body["position"] != "Driver" {
		t.Fatalf("set position from the list: %d %v", code, body)
	}
	if code, body, _ := h.admin("GET", fmt.Sprintf("/employees?position_id=%d", driverID), jwt, nil); code != 200 || len(body["items"].([]any)) != 1 {
		t.Fatalf("filter employees by position: %d %v", code, body)
	}
	if code, _, _ := h.admin("PUT", fmt.Sprintf("/positions/%d", driverID), jwt, map[string]any{"name": "Senior Driver"}); code != 200 {
		t.Fatalf("rename position: %d", code)
	}
	if _, body, _ := h.admin("GET", fmt.Sprintf("/employees/%d", dara.ID), jwt, nil); body["position"] != "Senior Driver" {
		t.Fatalf("renaming a position renames it on employees: %v", body["position"])
	}
	if code, body, _ := h.admin("DELETE", fmt.Sprintf("/positions/%d", driverID), jwt, nil); code != 409 || errCode(body) != "IN_USE" {
		t.Fatalf("a position in use cannot be deleted: %d %v", code, body)
	}
	code, body, _ = h.admin("POST", "/departments", jwt, map[string]any{"name": "Logistics", "branch_id": mainBranch})
	if code != 201 {
		t.Fatalf("create department: %d %v", code, body)
	}
	if code, _, _ := h.admin("DELETE", fmt.Sprintf("/departments/%d", uint64(num(body, "id"))), jwt, nil); code != 204 {
		t.Fatalf("delete an unused department: %d", code)
	}

	// ---------- leave types, policies and balances ----------
	h.set("2026-11-02 09:00") // Monday
	wfh := map[string]any{"branch_id": mainBranch, "code": "wfh", "name": "Work from home", "annual_days": 10, "approval": "AUTO", "color": "#0ea5e9"}
	if code, _, _ := h.admin("POST", "/leave/types", mgrJWT, wfh); code != 403 {
		t.Fatalf("managers cannot edit leave types, got %d", code)
	}
	code, body, _ = h.admin("POST", "/leave/types", jwt, wfh)
	if code != 201 || body["code"] != "WFH" {
		t.Fatalf("create leave type: %d %v", code, body)
	}
	wfhID := uint64(num(body, "id"))
	code, body, _ = h.app("POST", "/leave", daraTG, map[string]any{"leave_type_id": wfhID, "start_date": "2026-11-05", "end_date": "2026-11-05", "reason": "Plumber visit"})
	if code != 201 || body["status"] != models.LeaveApproved {
		t.Fatalf("an AUTO type is approved on submission: %d %v", code, body)
	}
	var wfhDay string
	h.db.QueryRow("SELECT status FROM attendance WHERE employee_id = ? AND work_date = '2026-11-05'", dara.ID).Scan(&wfhDay)
	if wfhDay != models.StatusLeave {
		t.Fatalf("auto-approved leave marks the day LEAVE, got %q", wfhDay)
	}
	if code, body, _ := h.admin("DELETE", fmt.Sprintf("/leave/types/%d", wfhID), jwt, nil); code != 409 || errCode(body) != "TYPE_IN_USE" {
		t.Fatalf("a used leave type can only be turned off: %d %v", code, body)
	}
	// HR gives Dara 3 days of annual leave this year.
	if code, body, _ := h.admin("PUT", "/leave/balances", jwt, map[string]any{"employee_id": dara.ID, "leave_type_id": annualID, "year": 2026, "allocated": 3, "carried": 0, "note": "Joined mid-year"}); code != 204 {
		t.Fatalf("set balance: %d %v", code, body)
	}
	four := map[string]any{"leave_type_id": annualID, "start_date": "2026-11-16", "end_date": "2026-11-19", "reason": "Trip"}
	if code, body, _ := h.app("POST", "/leave/preview", daraTG, four); code != 200 || errCodeOf(body["problem"]) != "INSUFFICIENT_BALANCE" {
		t.Fatalf("4 days with 3 left must be refused: %d %v", code, body)
	}
	if code, body, _ := h.app("POST", "/leave", daraTG, four); code != 400 || errCode(body) != "INSUFFICIENT_BALANCE" {
		t.Fatalf("submitting 4 days with 3 left must be refused: %d %v", code, body)
	}
	three := map[string]any{"leave_type_id": annualID, "start_date": "2026-11-16", "end_date": "2026-11-18", "reason": "Trip"}
	code, body, _ = h.app("POST", "/leave/preview", daraTG, three)
	if bal, _ := body["balance"].(map[string]any); code != 200 || bal["allocated"] != 3.0 || bal["available"] != 3.0 || bal["custom"] != true {
		t.Fatalf("preview shows the balance: %d %v", code, body)
	}
	if code, body, _ := h.app("POST", "/leave", daraTG, three); code != 201 {
		t.Fatalf("3 days fit: %d %v", code, body)
	}
	code, body, _ = h.app("GET", "/leave/balances", daraTG, nil)
	var annual map[string]any
	for _, b := range body["items"].([]any) {
		if b.(map[string]any)["code"] == "ANNUAL" {
			annual = b.(map[string]any)
		}
	}
	if code != 200 || annual == nil || annual["pending"] != 3.0 || annual["available"] != 0.0 {
		t.Fatalf("my balances count pending days: %d %v", code, annual)
	}
	code, body, _ = h.admin("GET", "/leave/balances?year=2026", jwt, nil)
	if code != 200 || len(body["items"].([]any)) == 0 || len(body["types"].([]any)) < 5 {
		t.Fatalf("balances report: %d %v", code, body)
	}
	if code, _, _ := h.admin("DELETE", fmt.Sprintf("/leave/balances?employee_id=%d&leave_type_id=%d&year=2026", dara.ID, annualID), jwt, nil); code != 204 {
		t.Fatalf("reset balance: %d", code)
	}
	if _, body, _ := h.app("GET", "/leave/balances", daraTG, nil); body["items"].([]any)[0].(map[string]any)["allocated"] != 18.0 {
		t.Fatalf("back to the type's 18 days: %v", body["items"].([]any)[0])
	}
	if code, _, _ := h.admin("PUT", fmt.Sprintf("/leave/types/%d", personalID), jwt, map[string]any{"max_days_per_request": 1, "min_notice_days": 7}); code != 200 {
		t.Fatalf("update policy: %d", code)
	}
	if code, body, _ := h.app("POST", "/leave/preview", daraTG, map[string]any{"leave_type_id": personalID, "start_date": "2026-11-23", "end_date": "2026-11-24", "reason": "x"}); code != 200 || errCodeOf(body["problem"]) != "TOO_MANY_DAYS" {
		t.Fatalf("max days per request: %d %v", code, body)
	}
	if code, body, _ := h.app("POST", "/leave/preview", daraTG, map[string]any{"leave_type_id": personalID, "start_date": "2026-11-04", "end_date": "2026-11-04", "reason": "x"}); code != 200 || errCodeOf(body["problem"]) != "SHORT_NOTICE" {
		t.Fatalf("notice period: %d %v", code, body)
	}

	// ---------- GPS rules per branch ----------
	if code, body, _ := h.admin("PUT", fmt.Sprintf("/branches/%d", srID), ownerJWT, map[string]any{"gps": map[string]any{"enabled": true, "required": true}}); code != 200 || body["gps_required"] != true {
		t.Fatalf("branch GPS rules: %d %v", code, body)
	}
	if code, body, _ := h.app("GET", "/attendance/today", bophaStaffTG, nil); code != 200 || body["check_in"].(map[string]any)["gps_required"] != true {
		t.Fatalf("Siem Reap staff must share their location: %d %v", code, body)
	}
	if code, body, _ := h.app("POST", "/attendance/check-in", bophaStaffTG, map[string]string{"photo": photo("bopha-gps")}); code != 400 || errCode(body) != "LOCATION_REQUIRED" {
		t.Fatalf("no location in a GPS-required branch: %d %v", code, body)
	}
	if _, body, _ := h.app("GET", "/attendance/today", daraTG, nil); body["check_in"].(map[string]any)["need_gps"] != false {
		t.Fatalf("other branches keep their own rules (off): %v", body["check_in"])
	}
	if code, body, _ := h.admin("PUT", fmt.Sprintf("/branches/%d", srID), ownerJWT, map[string]any{"gps": map[string]any{"enabled": false}}); code != 200 || body["gps_enabled"] != false {
		t.Fatalf("GPS off: %d %v", code, body)
	}

	// ---------- a client's Company Admin: creates their own branch and sees only that ----------
	code, body, _ = h.admin("POST", "/system-users", ownerJWT, map[string]any{"full_name": "Client Admin", "email": "client@example.com",
		"password": "client-password-1", "role": "company_admin"})
	if code != 201 || body["scope"] != "branches" && body["scope"] != "team" {
		t.Fatalf("Company Admin without a branch yet: %d %v", code, body)
	}
	clientJWT, _ := h.login("client@example.com", "client-password-1")
	if _, body, _ := h.admin("GET", "/branches", clientJWT, nil); len(body["items"].([]any)) != 0 {
		t.Fatalf("no branch yet = sees no branch (not every branch): %v", body)
	}
	code, body, _ = h.admin("POST", "/branches", clientJWT, map[string]any{"code": "KCC", "name": "Kampot Coffee Co."})
	if code != 201 {
		t.Fatalf("a Company Admin creates their own branch: %d %v", code, body)
	}
	kccID := uint64(num(body, "id"))
	if _, body, _ := h.admin("GET", "/branches", clientJWT, nil); len(body["items"].([]any)) != 1 {
		t.Fatalf("the branch they created is the one they see: %v", body)
	}
	if code, _, _ := h.admin("PUT", fmt.Sprintf("/branches/%d", mainBranch), clientJWT, map[string]any{"name": "Mine"}); code != 403 {
		t.Fatalf("other branches are out of reach: %d", code)
	}
	if code, body, _ := h.admin("PUT", fmt.Sprintf("/branches/%d", kccID), clientJWT, map[string]any{"name": "Kampot Coffee", "gps": map[string]any{"enabled": true}}); code != 200 || body["name"] != "Kampot Coffee" {
		t.Fatalf("they edit their own branch (name, GPS): %d %v", code, body)
	}
	// Their branch starts empty: they set it up themselves.
	if _, body, _ := h.admin("GET", fmt.Sprintf("/work-schedules?branch_id=%d", kccID), ownerJWT, nil); len(body["items"].([]any)) != 0 {
		t.Fatalf("a new branch has no shifts (nothing is shared): %v", body)
	}
	// …unless they ask for the starter setup: a default shift and the usual leave types.
	code, body, _ = h.admin("POST", "/branches", clientJWT, map[string]any{"code": "KCR", "name": "Kampot Riverside", "starter": true})
	if code != 201 {
		t.Fatalf("second branch with the starter setup: %d %v", code, body)
	}
	kcrID := uint64(num(body, "id"))
	if _, body, _ := h.admin("GET", fmt.Sprintf("/work-schedules?branch_id=%d", kcrID), clientJWT, nil); len(body["items"].([]any)) != 1 || body["items"].([]any)[0].(map[string]any)["is_default"] != true {
		t.Fatalf("starter: one default shift: %v", body)
	}
	if _, body, _ := h.admin("GET", fmt.Sprintf("/leave/types?all=1&branch_id=%d", kcrID), clientJWT, nil); len(body["items"].([]any)) != 5 {
		t.Fatalf("starter: the usual leave types: %v", body)
	}
	if _, body, _ := h.admin("GET", "/branches", clientJWT, nil); len(body["items"].([]any)) != 2 {
		t.Fatalf("they see both branches they created: %v", body)
	}

	// ---------- a Company Admin manages the admin accounts of their own branches ----------
	if _, body, _ := h.admin("GET", "/system-users", clientJWT, nil); len(body["items"].([]any)) != 1 || body["items"].([]any)[0].(map[string]any)["self"] != true {
		t.Fatalf("they see only accounts of their branches (just themselves so far): %v", body)
	}
	assignable := map[string]bool{}
	_, rolesBody, _ := h.admin("GET", "/roles", clientJWT, nil)
	for _, r := range rolesBody["items"].([]any) {
		rm := r.(map[string]any)
		assignable[fmt.Sprint(rm["code"])] = rm["assignable"] == true
	}
	if !assignable["branch_admin"] || !assignable["company_admin"] || assignable["hr_admin"] || assignable["super_admin"] || assignable["staff"] {
		t.Fatalf("roles they may give: branch-level admin roles within their powers only: %v", assignable)
	}
	newAdmin := func(email, role string, branch uint64) (int, map[string]any) {
		code, body, _ := h.admin("POST", "/system-users", clientJWT, map[string]any{"full_name": "Kampot " + role, "email": email,
			"password": "kampot-pass-123", "role": role, "branch_id": branch})
		return code, body
	}
	code, body = newAdmin("kcr.admin@example.com", "branch_admin", kcrID)
	if code != 201 || num(body, "branch_id") != float64(kcrID) {
		t.Fatalf("a Branch Admin for their own branch: %d %v", code, body)
	}
	kcrAdmin := uint64(num(body, "id"))
	if _, b, _ := h.admin("GET", fmt.Sprintf("/branches/%d", kcrID), clientJWT, nil); b["admin"] == nil {
		t.Fatalf("…who becomes that branch's admin: %v", b)
	}
	if code, body := newAdmin("x1@example.com", "branch_admin", mainBranch); code != 403 {
		t.Fatalf("not for someone else's branch: %d %v", code, body)
	}
	if code, body := newAdmin("x2@example.com", "hr_admin", 0); code != 403 {
		t.Fatalf("no company-wide roles: %d %v", code, body)
	}
	if code, body := newAdmin("x3@example.com", "company_admin", 0); code != 400 || errCode(body) != "BRANCH_REQUIRED" {
		t.Fatalf("every account they make works in one of their branches: %d %v", code, body)
	}
	code, body = newAdmin("kcc.partner@example.com", "company_admin", kccID)
	if code != 201 {
		t.Fatalf("another Company Admin for their branch: %d %v", code, body)
	}
	partner := uint64(num(body, "id"))
	if code, _, _ := h.admin("PUT", fmt.Sprintf("/system-users/%d/branches", partner), clientJWT, map[string]any{"branch_ids": []uint64{mainBranch}}); code != 403 {
		t.Fatalf("they hand out only their own branches: %d", code)
	}
	if code, _, _ := h.admin("PUT", fmt.Sprintf("/system-users/%d/branches", partner), clientJWT, map[string]any{"branch_ids": []uint64{kcrID}}); code != 200 {
		t.Fatalf("their own branches, yes: %d", code)
	}
	if code, _, _ := h.admin("POST", fmt.Sprintf("/system-users/%d/password", kcrAdmin), clientJWT, map[string]any{"password": "new-kampot-pass-1"}); code != 204 && code != 200 {
		t.Fatalf("reset the password of their branch admin: %d", code)
	}
	if _, body, _ := h.admin("GET", "/system-users", clientJWT, nil); len(body["items"].([]any)) != 3 {
		t.Fatalf("now they see their 3 accounts: %v", body)
	}
	if _, body, _ := h.admin("GET", "/system-users", ownerJWT, nil); len(body["items"].([]any)) < 6 {
		t.Fatalf("the owner still sees every account: %v", body)
	}
	var ownerLogin uint64
	h.db.QueryRow("SELECT u.id FROM users u JOIN employees e ON e.id = u.employee_id WHERE e.role_id = 1 LIMIT 1").Scan(&ownerLogin)
	if code, _, _ := h.admin("PUT", fmt.Sprintf("/system-users/%d", ownerLogin), clientJWT, map[string]any{"status": "INACTIVE"}); code != 404 {
		t.Fatalf("accounts outside their branches don't exist for them: %d", code)
	}
	if code, _, _ := h.admin("DELETE", fmt.Sprintf("/system-users/%d", partner), clientJWT, nil); code != 204 && code != 200 {
		t.Fatalf("they remove an account of theirs: %d", code)
	}
	// Role management: they make roles for their branches, with permissions they have.
	_, cat, _ := h.admin("GET", "/roles/permissions", clientJWT, nil)
	grantable := map[string]bool{}
	for _, p := range cat["items"].([]any) {
		grantable[fmt.Sprint(p.(map[string]any)["code"])] = true
	}
	if !grantable["team.review"] || !grantable["attendance.self"] || grantable["all.view"] || grantable["users.manage"] || grantable["settings.edit"] {
		t.Fatalf("they can only give permissions they have: %v", grantable)
	}
	if code, body, _ := h.admin("POST", "/roles", clientJWT, map[string]any{"name": "Shift Lead", "permissions": []string{"team.view"}}); code != 400 || errCode(body) != "BRANCH_REQUIRED" {
		t.Fatalf("a role of theirs belongs to one of their branches: %d %v", code, body)
	}
	if code, _, _ := h.admin("POST", "/roles", clientJWT, map[string]any{"name": "Spy", "branch_id": kccID, "permissions": []string{"all.view"}}); code != 403 {
		t.Fatalf("no permissions they don't have: %d", code)
	}
	code, body, _ = h.admin("POST", "/roles", clientJWT, map[string]any{"name": "Shift Lead", "branch_id": kccID,
		"permissions": []string{"attendance.self", "leave.self", "team.view", "team.review"}})
	if code != 201 {
		t.Fatalf("a role for their branch: %d %v", code, body)
	}
	shiftLead := fmt.Sprint(body["code"])
	shiftLeadID := uint64(num(body, "id"))
	if code, body, _ := h.admin("POST", "/employees", clientJWT, map[string]any{"employee_code": "KC09", "full_name": "Lead One", "role": shiftLead, "branch_id": kccID}); code != 201 {
		t.Fatalf("their staff take their branch's role: %d %v", code, body)
	}
	if code, body, _ := h.admin("POST", "/employees", ownerJWT, map[string]any{"employee_code": "MX09", "full_name": "Wrong Branch", "role": shiftLead, "branch_id": mainBranch}); code != 400 || errCode(body) != "ROLE_OTHER_BRANCH" {
		t.Fatalf("a branch's role is not for another branch: %d %v", code, body)
	}
	seen := func(tok string) map[string]bool {
		_, body, _ := h.admin("GET", "/roles", tok, nil)
		out := map[string]bool{}
		for _, r := range body["items"].([]any) {
			out[fmt.Sprint(r.(map[string]any)["code"])] = true
		}
		return out
	}
	kcrJWT, _ := h.login("kcr.admin@example.com", "new-kampot-pass-1") // the Branch Admin of their other branch
	if !seen(clientJWT)[shiftLead] || seen(kcrJWT)[shiftLead] || !seen(ownerJWT)[shiftLead] {
		t.Fatal("a branch's role is seen in that branch (and by the owner) only")
	}
	// A Branch Admin runs Administration for their branch too.
	if code, body, _ := h.admin("GET", "/system-users", kcrJWT, nil); code != 200 || len(body["items"].([]any)) != 1 {
		t.Fatalf("a Branch Admin sees their branch's accounts (just themselves): %d %v", code, body)
	}
	if code, body, _ := h.admin("POST", "/roles", kcrJWT, map[string]any{"name": "Riverside Lead", "branch_id": kcrID, "permissions": []string{"team.view"}}); code != 201 {
		t.Fatalf("a Branch Admin makes roles for their branch: %d %v", code, body)
	}
	if code, _, _ := h.admin("POST", "/roles", kcrJWT, map[string]any{"name": "Elsewhere", "branch_id": kccID, "permissions": []string{"team.view"}}); code != 400 {
		t.Fatalf("not for a branch they don't run: %d", code)
	}
	if code, _, _ := h.admin("PUT", fmt.Sprintf("/roles/%d", shiftLeadID), clientJWT, map[string]any{"permissions": []string{"attendance.self", "team.view"}}); code != 200 {
		t.Fatalf("they edit their branch's role: %d", code)
	}
	if code, _, _ := h.admin("PUT", "/roles/2", clientJWT, map[string]any{"name": "Mine now"}); code != 404 {
		t.Fatalf("company roles are not theirs: %d", code)
	}

	// ---------- lists come 10 at a time ----------
	w, paged := h.do("GET", "/api/v1/audit-logs", nil, withHeader("Authorization", "Bearer "+ownerJWT))
	if w.Code != 200 || len(paged["items"].([]any)) != 10 || num(paged, "page_size") != 10 || num(paged, "total") <= 10 {
		t.Fatalf("default page: 10 of %v", paged["total"])
	}
	w, paged = h.do("GET", "/api/v1/work-schedules?page=2&page_size=1", nil, withHeader("Authorization", "Bearer "+ownerJWT))
	if w.Code != 200 || len(paged["items"].([]any)) != 1 || num(paged, "page") != 2 {
		t.Fatalf("page 2 of 1-item pages: %v", paged)
	}
	w, paged = h.do("GET", "/api/v1/employees", nil, withHeader("Authorization", "Bearer "+ownerJWT))
	if w.Code != 200 || num(paged, "page_size") != 10 || len(paged["items"].([]any)) > 10 {
		t.Fatalf("employees: 10 per page by default: %v", paged["page_size"])
	}
	kccShift := map[string]any{"branch_id": kccID, "name": "Café Hours", "start_time": "07:00", "end_time": "16:00", "work_type": "NORMAL", "grace_minutes": 5, "work_days": []int{1, 2, 3, 4, 5, 6}, "color": "#b45309"}
	if code, body, _ := h.admin("POST", "/work-schedules", clientJWT, kccShift); code != 201 || body["is_default"] != true {
		t.Fatalf("their first shift is their branch's default: %d %v", code, body)
	}
	if code, _, _ := h.admin("POST", "/departments", clientJWT, map[string]any{"name": "Café", "branch_id": kccID}); code != 201 {
		t.Fatalf("their own department: %d", code)
	}
	if code, _, _ := h.admin("POST", "/departments", clientJWT, map[string]any{"name": "Café", "branch_id": mainBranch}); code != 403 {
		t.Fatalf("not in another branch: %d", code)
	}
	if code, _, _ := h.admin("POST", "/leave/types", clientJWT, map[string]any{"branch_id": kccID, "code": "ANNUAL", "name": "Annual Leave", "annual_days": 12}); code != 201 {
		t.Fatalf("their own leave type (same code as Main's is fine): %d", code)
	}
	if code, _, _ := h.admin("POST", "/holidays", clientJWT, map[string]any{"branch_ids": []uint64{kccID, mainBranch}, "date": "2026-12-25", "name": "Christmas"}); code != 403 {
		t.Fatalf("holidays only for their branches: %d", code)
	}
	if code, _, _ := h.admin("POST", "/holidays", clientJWT, map[string]any{"branch_ids": []uint64{kccID}, "date": "2026-12-25", "name": "Christmas"}); code != 201 {
		t.Fatalf("their own holiday: %d", code)
	}
	if code, body, _ := h.admin("POST", "/employees", clientJWT, map[string]any{"employee_code": "KC01", "full_name": "Mony Prak"}); code != 400 || errCode(body) != "BRANCH_REQUIRED" {
		t.Fatalf("with two branches they choose one: %d %v", code, body)
	}
	code, body, _ = h.admin("POST", "/employees", clientJWT, map[string]any{"employee_code": "KC01", "full_name": "Mony Prak", "department": "Café", "branch_id": kccID})
	if code != 201 || num(body, "branch_id") != float64(kccID) || body["work_schedule"].(map[string]any)["name"] != "Café Hours" {
		t.Fatalf("their staff land in their branch with its default shift: %d %v", code, body)
	}
	visibleTo := func(tok string) string {
		_, body, _ := h.admin("GET", "/employees", tok, nil)
		var codes []string
		for _, e := range body["items"].([]any) {
			codes = append(codes, fmt.Sprint(e.(map[string]any)["employee_code"]))
		}
		return strings.Join(codes, ",")
	}
	if v := visibleTo(clientJWT); v != "KC01,KC09" {
		t.Fatalf("the client sees only their staff: %s", v)
	}
	for _, path := range []string{"/departments", "/positions", "/work-schedules", "/leave/types?all=1", "/holidays"} {
		_, body, _ := h.admin("GET", path, clientJWT, nil)
		for _, it := range body["items"].([]any) {
			if b := uint64(num(it.(map[string]any), "branch_id")); b != kccID && b != kcrID { // only their two branches
				t.Fatalf("%s shows another branch's item to the client: %v", path, it)
			}
		}
	}
	if !strings.Contains(visibleTo(ownerJWT), "KC01") {
		t.Fatal("the owner sees every branch, the client's too")
	}

	// ---------- deleting an attendance record (reason required, audited, photos removed) ----------
	var delID uint64
	var delPhotos int
	h.db.QueryRow("SELECT a.id, (SELECT COUNT(*) FROM attendance_photos p WHERE p.attendance_id = a.id) FROM attendance a WHERE a.employee_id = ? AND a.check_in_at IS NOT NULL ORDER BY a.id LIMIT 1", dara.ID).Scan(&delID, &delPhotos)
	objects := len(store.objs)
	if code, body, _ := h.admin("DELETE", fmt.Sprintf("/attendance/%d", delID), mgrJWT, map[string]any{"reason": "x"}); code != 403 {
		t.Fatalf("deleting needs attendance.edit: %d %v", code, body)
	}
	if code, body, _ := h.admin("DELETE", fmt.Sprintf("/attendance/%d", delID), jwt, map[string]any{"reason": " "}); code != 400 {
		t.Fatalf("a reason is required to delete: %d %v", code, body)
	}
	if code, body, _ := h.admin("DELETE", fmt.Sprintf("/attendance/%d", delID), jwt, map[string]any{"reason": "Duplicate test record"}); code != 204 {
		t.Fatalf("delete attendance: %d %v", code, body)
	}
	var left, children int
	h.db.QueryRow("SELECT COUNT(*) FROM attendance WHERE id = ?", delID).Scan(&left)
	h.db.QueryRow("SELECT (SELECT COUNT(*) FROM attendance_confirmations WHERE attendance_id = ?) + (SELECT COUNT(*) FROM attendance_locations WHERE attendance_id = ?) + (SELECT COUNT(*) FROM attendance_photos WHERE attendance_id = ?)", delID, delID, delID).Scan(&children)
	if left != 0 || children != 0 || len(store.objs) != objects-delPhotos {
		t.Fatalf("delete removes the record, its reviews/locations/photos and the photo files: left=%d children=%d objects %d→%d (photos %d)", left, children, objects, len(store.objs), delPhotos)
	}
	var delAudit string
	h.db.QueryRow("SELECT JSON_UNQUOTE(JSON_EXTRACT(details, '$.reason')) FROM audit_logs WHERE action = 'attendance.delete' AND entity_id = ?", delID).Scan(&delAudit)
	if delAudit != "Duplicate test record" {
		t.Fatalf("delete is audited with the reason: %q", delAudit)
	}
	if code, _, _ := h.admin("DELETE", fmt.Sprintf("/attendance/%d", delID), jwt, map[string]any{"reason": "again"}); code != 404 {
		t.Fatalf("deleting twice: %d", code)
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
