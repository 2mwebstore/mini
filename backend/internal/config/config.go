// Package config loads runtime configuration from environment variables.
// Secrets are never hard-coded; see .env.example for the full list. When the API
// runs outside Docker (`go run ./cmd/api serve` in backend/), it also reads
// backend/.env and then the project's root .env (variables already set win).
package config

import (
	"bufio"
	"errors"
	"fmt"
	"io/fs"
	"net"
	"os"
	"strings"
	"time"

	mysqldrv "github.com/go-sql-driver/mysql"
)

type Config struct {
	Env      string // development | production
	HTTPAddr string

	// Built from MYSQL_HOST, MYSQL_PORT, MYSQL_DATABASE, MYSQL_USER, MYSQL_PASSWORD.
	// (A full DATABASE_URL DSN, if set, is used as-is instead.)
	DatabaseURL string
	RedisURL    string // redis://:password@redis:6379/0

	JWTSecret string
	JWTTTL    time.Duration

	// Telegram (bot token, Mini App URL, chats) is configured in the admin panel and
	// stored in company_settings. Only these optional tunables remain here:
	MiniAppSessionTTL time.Duration // how long a Mini App launch stays valid (default 24h)
	TelegramAPIBase   string        // override for tests, default https://api.telegram.org

	// Written by the backup container; when present, failed backups are reported.
	BackupStatusFile string

	StorageEndpoint  string // host:port, e.g. minio:9000 or <account>.r2.cloudflarestorage.com
	StorageAccessKey string
	StorageSecretKey string
	StorageBucket    string
	StorageRegion    string
	StorageUseSSL    bool

	CORSOrigins []string
}

func Load() (*Config, error) {
	if err := loadEnvFile(".env"); err != nil {
		return nil, err
	}
	// In backend/ of the project, the root .env holds the shared settings (MySQL…).
	if _, err := os.Stat("../docker-compose.yml"); err == nil {
		if err := loadEnvFile("../.env"); err != nil {
			return nil, err
		}
	}
	c := &Config{
		Env:               get("APP_ENV", "production"),
		HTTPAddr:          get("HTTP_ADDR", ":8080"),
		DatabaseURL:       os.Getenv("DATABASE_URL"),
		RedisURL:          get("REDIS_URL", "redis://redis:6379/0"),
		JWTSecret:         os.Getenv("JWT_SECRET"),
		JWTTTL:            getDuration("JWT_TTL", 12*time.Hour),
		MiniAppSessionTTL: getDuration("MINIAPP_SESSION_TTL", 24*time.Hour),
		TelegramAPIBase:   get("TELEGRAM_API_BASE", "https://api.telegram.org"),
		BackupStatusFile:  get("BACKUP_STATUS_FILE", "/backups/.last_backup.json"),
		StorageEndpoint:   get("STORAGE_ENDPOINT", "minio:9000"),
		StorageAccessKey:  os.Getenv("STORAGE_ACCESS_KEY"),
		StorageSecretKey:  os.Getenv("STORAGE_SECRET_KEY"),
		StorageBucket:     get("STORAGE_BUCKET", "attendance"),
		StorageRegion:     get("STORAGE_REGION", "us-east-1"),
		StorageUseSSL:     get("STORAGE_USE_SSL", "false") == "true",
		CORSOrigins:       splitCSV(os.Getenv("CORS_ORIGINS")),
	}
	var missing []string
	if c.DatabaseURL == "" {
		for _, k := range []string{"MYSQL_DATABASE", "MYSQL_USER"} {
			if os.Getenv(k) == "" {
				missing = append(missing, k)
			}
		}
		c.DatabaseURL = mysqlDSN()
	}
	if c.JWTSecret == "" {
		missing = append(missing, "JWT_SECRET")
	}
	if len(missing) > 0 {
		return nil, fmt.Errorf("missing required env: %s", strings.Join(missing, ", "))
	}
	if len(c.JWTSecret) < 32 {
		return nil, fmt.Errorf("JWT_SECRET must be at least 32 characters")
	}
	return c, nil
}

func (c *Config) IsProduction() bool { return c.Env == "production" }

func get(k, def string) string {
	if v := strings.TrimSpace(os.Getenv(k)); v != "" {
		return v
	}
	return def
}

func getDuration(k string, def time.Duration) time.Duration {
	if v, err := time.ParseDuration(get(k, "")); err == nil {
		return v
	}
	return def
}

func splitCSV(s string) []string {
	var out []string
	for _, p := range strings.Split(s, ",") {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}

// mysqlDSN builds the driver DSN from the MYSQL_* variables (the driver escapes
// passwords with special characters correctly).
func mysqlDSN() string {
	d := mysqldrv.NewConfig()
	d.User = os.Getenv("MYSQL_USER")
	d.Passwd = os.Getenv("MYSQL_PASSWORD")
	d.Net = "tcp"
	d.Addr = net.JoinHostPort(get("MYSQL_HOST", "127.0.0.1"), get("MYSQL_PORT", "3306"))
	d.DBName = os.Getenv("MYSQL_DATABASE")
	d.ParseTime = true
	return d.FormatDSN()
}

// loadEnvFile sets variables from a KEY=VALUE file for local development.
// Variables already set in the environment win; a missing file is ignored
// (containers never have one: .dockerignore excludes it).
func loadEnvFile(path string) error {
	f, err := os.Open(path)
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	for n := 1; sc.Scan(); n++ {
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		k, v, ok := strings.Cut(strings.TrimPrefix(line, "export "), "=")
		if !ok {
			return fmt.Errorf("%s line %d: expected KEY=VALUE", path, n)
		}
		k, v = strings.TrimSpace(k), strings.TrimSpace(v)
		if len(v) >= 2 && (v[0] == '"' && v[len(v)-1] == '"' || v[0] == '\'' && v[len(v)-1] == '\'') {
			v = v[1 : len(v)-1]
		}
		if _, set := os.LookupEnv(k); !set {
			if err := os.Setenv(k, v); err != nil {
				return err
			}
		}
	}
	return sc.Err()
}
