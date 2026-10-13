package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	mysqldrv "github.com/go-sql-driver/mysql"
)

func TestLoadEnvFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), ".env")
	content := `# local development
DATABASE_URL=attendance:attendance@tcp(127.0.0.1:3307)/attendance?parseTime=true
export REDIS_URL="redis://127.0.0.1:6379/0"
JWT_SECRET='from-file'

APP_ENV = development
`
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("JWT_SECRET", "from-shell") // the shell wins over the file
	for _, k := range []string{"DATABASE_URL", "REDIS_URL", "APP_ENV"} {
		t.Setenv(k, "")
		os.Unsetenv(k)
	}
	if err := loadEnvFile(path); err != nil {
		t.Fatal(err)
	}
	want := map[string]string{
		"DATABASE_URL": "attendance:attendance@tcp(127.0.0.1:3307)/attendance?parseTime=true",
		"REDIS_URL":    "redis://127.0.0.1:6379/0",
		"JWT_SECRET":   "from-shell",
		"APP_ENV":      "development",
	}
	for k, v := range want {
		if got := os.Getenv(k); got != v {
			t.Errorf("%s = %q, want %q", k, got, v)
		}
	}
	if err := loadEnvFile(filepath.Join(t.TempDir(), "missing")); err != nil {
		t.Errorf("a missing file is not an error: %v", err)
	}
	bad := filepath.Join(t.TempDir(), "bad")
	os.WriteFile(bad, []byte("NOT A PAIR\n"), 0o600)
	if err := loadEnvFile(bad); err == nil {
		t.Error("malformed lines must be reported")
	}
}

func TestDatabaseFromMySQLVars(t *testing.T) {
	wd, _ := os.Getwd()
	if err := os.Chdir(t.TempDir()); err != nil { // no .env files here
		t.Fatal(err)
	}
	t.Cleanup(func() { os.Chdir(wd) })
	t.Setenv("DATABASE_URL", "")
	t.Setenv("JWT_SECRET", "a-test-secret-that-is-long-enough-123")
	t.Setenv("MYSQL_HOST", "127.0.0.1")
	t.Setenv("MYSQL_PORT", "3306")
	t.Setenv("MYSQL_DATABASE", "attendance")
	t.Setenv("MYSQL_USER", "root")
	t.Setenv("MYSQL_PASSWORD", "p@ss:w/rd")
	c, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	d, err := mysqldrv.ParseDSN(c.DatabaseURL)
	if err != nil {
		t.Fatalf("built DSN must parse: %v (%s)", err, c.DatabaseURL)
	}
	if d.User != "root" || d.Passwd != "p@ss:w/rd" || d.Addr != "127.0.0.1:3306" || d.DBName != "attendance" || !d.ParseTime {
		t.Fatalf("unexpected DSN fields: %+v", d)
	}

	t.Setenv("MYSQL_USER", "")
	if _, err := Load(); err == nil || !strings.Contains(err.Error(), "MYSQL_USER") {
		t.Fatalf("missing MYSQL_USER must be reported, got %v", err)
	}
}

// Railway-style variables: PORT, a URL storage endpoint, proxies, built front ends.
func TestPlatformVars(t *testing.T) {
	wd, _ := os.Getwd()
	if err := os.Chdir(t.TempDir()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.Chdir(wd) })
	t.Setenv("DATABASE_URL", "u:p@tcp(db:3306)/x")
	t.Setenv("JWT_SECRET", "a-test-secret-that-is-long-enough-123")
	t.Setenv("HTTP_ADDR", "")
	t.Setenv("PORT", "5123")
	t.Setenv("STORAGE_ENDPOINT", "https://t3.storageapi.dev")
	t.Setenv("STORAGE_USE_SSL", "")
	t.Setenv("STORAGE_URL_STYLE", "virtual")
	t.Setenv("TRUSTED_PROXIES", "100.64.0.0/10, 127.0.0.1")
	web := t.TempDir()
	t.Setenv("WEB_DIR", web)
	for _, app := range []string{"admin", "app"} {
		os.MkdirAll(filepath.Join(web, app), 0o755)
		os.WriteFile(filepath.Join(web, app, "200.html"), []byte("<html>"), 0o644)
	}
	c, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if c.HTTPAddr != ":5123" || c.StorageEndpoint != "t3.storageapi.dev" || !c.StorageUseSSL || c.StorageURLStyle != "virtual" ||
		strings.Join(c.TrustedProxies, " ") != "100.64.0.0/10 127.0.0.1" || c.WebDir != web {
		t.Fatalf("unexpected: %+v", c)
	}

	t.Setenv("HTTP_ADDR", "127.0.0.1:9000") // explicit address wins over PORT
	t.Setenv("STORAGE_ENDPOINT", "http://127.0.0.1:9090")
	if c, err = Load(); err != nil || c.HTTPAddr != "127.0.0.1:9000" || c.StorageEndpoint != "127.0.0.1:9090" || c.StorageUseSSL {
		t.Fatalf("got %+v, %v", c, err)
	}

	for k, v := range map[string]string{"STORAGE_URL_STYLE": "dns", "TRUSTED_PROXIES": "railway", "STORAGE_ENDPOINT": "ftp://x", "WEB_DIR": t.TempDir()} {
		t.Run(k, func(t *testing.T) {
			t.Setenv(k, v)
			if _, err := Load(); err == nil || !strings.Contains(err.Error(), k) {
				t.Fatalf("%s=%s must be rejected, got %v", k, v, err)
			}
		})
	}
}
