// Package database opens MySQL connections and applies embedded SQL migrations.
package database

import (
	"context"
	"database/sql"
	"fmt"
	"io/fs"
	"log"
	"log/slog"
	"os"
	"regexp"
	"sort"
	"strings"
	"time"

	"attendance/backend/migrations"

	mysqldrv "github.com/go-sql-driver/mysql"
	"gorm.io/driver/mysql"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

// Open returns a GORM handle. The DSN is normalised to always use UTC, parseTime
// and utf8mb4 so that time handling is consistent regardless of what the operator set.
func Open(dsn string, debug bool) (*gorm.DB, error) {
	cfg, err := normalize(dsn)
	if err != nil {
		return nil, err
	}
	lvl := logger.Warn
	if debug {
		lvl = logger.Info
	}
	db, err := gorm.Open(mysql.Open(cfg.FormatDSN()), &gorm.Config{
		Logger: logger.New(log.New(os.Stdout, "", log.LstdFlags), logger.Config{
			SlowThreshold: 500 * time.Millisecond, LogLevel: lvl, IgnoreRecordNotFoundError: true, Colorful: false,
		}),
		NowFunc:                func() time.Time { return time.Now().UTC() },
		SkipDefaultTransaction: true,
	})
	if err != nil {
		return nil, err
	}
	sqlDB, err := db.DB()
	if err != nil {
		return nil, err
	}
	sqlDB.SetMaxOpenConns(25)
	sqlDB.SetMaxIdleConns(10)
	sqlDB.SetConnMaxLifetime(30 * time.Minute)
	return db, nil
}

func normalize(dsn string) (*mysqldrv.Config, error) {
	cfg, err := mysqldrv.ParseDSN(dsn)
	if err != nil {
		return nil, fmt.Errorf("invalid DATABASE_URL: %w", err)
	}
	cfg.ParseTime = true
	cfg.Loc = time.UTC
	if cfg.Params == nil {
		cfg.Params = map[string]string{}
	}
	cfg.Params["charset"] = "utf8mb4"
	cfg.Params["time_zone"] = "'+00:00'"
	cfg.MultiStatements = false
	return cfg, nil
}

// MigrateResult says what Migrate did.
type MigrateResult struct {
	Database string   // host:port/name (no credentials)
	Created  bool     // the database did not exist and was created
	Applied  []string // versions applied by this run
	Total    int      // migrations known to this build
	Latest   string   // newest version now in the database
}

// Migrate applies every embedded *.sql file that has not been applied yet.
// Migrations run on a dedicated connection with multi-statement support; the
// application connection keeps it disabled.
func Migrate(ctx context.Context, dsn string) (*MigrateResult, error) {
	cfg, err := normalize(dsn)
	if err != nil {
		return nil, err
	}
	res := &MigrateResult{Database: cfg.Addr + "/" + cfg.DBName}
	if res.Created, err = ensureDatabase(ctx, cfg); err != nil {
		return nil, err
	}
	cfg.MultiStatements = true
	db, err := sql.Open("mysql", cfg.FormatDSN())
	if err != nil {
		return nil, err
	}
	defer db.Close()

	if err := waitFor(ctx, db); err != nil {
		return nil, err
	}
	if _, err := db.ExecContext(ctx, `CREATE TABLE IF NOT EXISTS schema_migrations (
		version VARCHAR(128) NOT NULL PRIMARY KEY,
		applied_at DATETIME(3) NOT NULL DEFAULT CURRENT_TIMESTAMP(3)
	) ENGINE=InnoDB`); err != nil {
		return nil, fmt.Errorf("create schema_migrations: %w", err)
	}

	applied := map[string]bool{}
	rows, err := db.QueryContext(ctx, `SELECT version FROM schema_migrations`)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var v string
		if err := rows.Scan(&v); err != nil {
			rows.Close()
			return nil, err
		}
		applied[v] = true
	}
	rows.Close()

	files, err := fs.Glob(migrations.FS, "*.sql")
	if err != nil {
		return nil, err
	}
	sort.Strings(files)
	res.Total = len(files)
	for _, f := range files {
		version := strings.TrimSuffix(f, ".sql")
		res.Latest = version
		if applied[version] {
			continue
		}
		body, err := migrations.FS.ReadFile(f)
		if err != nil {
			return nil, err
		}
		slog.Info("applying migration", "version", version)
		if _, err := db.ExecContext(ctx, string(body)); err != nil {
			return nil, fmt.Errorf("migration %s failed: %w", version, err)
		}
		if _, err := db.ExecContext(ctx, `INSERT INTO schema_migrations (version) VALUES (?)`, version); err != nil {
			return nil, err
		}
		res.Applied = append(res.Applied, version)
	}
	return res, nil
}

var dbNameRe = regexp.MustCompile(`^[A-Za-z0-9_$-]{1,64}$`)

// ensureDatabase creates the database when the server doesn't have it yet (e.g. a
// fresh MAMP or MySQL install), so migrations work without creating it by hand.
func ensureDatabase(ctx context.Context, cfg *mysqldrv.Config) (bool, error) {
	if cfg.DBName == "" {
		return false, nil
	}
	server := cfg.Clone()
	server.DBName = ""
	db, err := sql.Open("mysql", server.FormatDSN())
	if err != nil {
		return false, err
	}
	defer db.Close()
	if err := waitFor(ctx, db); err != nil {
		return false, err
	}
	var n int
	if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM information_schema.SCHEMATA WHERE SCHEMA_NAME = ?`, cfg.DBName).Scan(&n); err != nil {
		return false, err
	}
	if n > 0 {
		return false, nil
	}
	if !dbNameRe.MatchString(cfg.DBName) {
		return false, fmt.Errorf("database %q does not exist (and its name is not one we create automatically)", cfg.DBName)
	}
	if _, err := db.ExecContext(ctx, "CREATE DATABASE `"+cfg.DBName+"` CHARACTER SET utf8mb4 COLLATE utf8mb4_unicode_ci"); err != nil {
		return false, fmt.Errorf("database %q does not exist and could not be created: %w", cfg.DBName, err)
	}
	slog.Info("created database", "database", cfg.DBName)
	return true, nil
}

// waitFor retries until the database accepts connections (useful on first docker start).
func waitFor(ctx context.Context, db *sql.DB) error {
	var err error
	for i := 0; i < 30; i++ {
		if err = db.PingContext(ctx); err == nil {
			return nil
		}
		slog.Warn("database not ready, retrying", "err", err)
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(2 * time.Second):
		}
	}
	return fmt.Errorf("database unreachable: %w", err)
}
