// Command api runs the attendance REST API and scheduler.
//
//	api serve          # default: migrate, then serve HTTP + scheduler
//	api migrate        # apply migrations and exit
//	api create-admin   # bootstrap the first super admin (see README)
//	api seed-demo      # fill a dev database with sample branches, staff and attendance
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"attendance/backend/internal/access"
	"attendance/backend/internal/attendance"
	"attendance/backend/internal/auth"
	"attendance/backend/internal/branch"
	"attendance/backend/internal/config"
	"attendance/backend/internal/database"
	"attendance/backend/internal/demo"
	"attendance/backend/internal/employee"
	"attendance/backend/internal/httpapi"
	"attendance/backend/internal/leave"
	"attendance/backend/internal/models"
	"attendance/backend/internal/notification"
	"attendance/backend/internal/report"
	"attendance/backend/internal/schedule"
	"attendance/backend/internal/scheduler"
	"attendance/backend/internal/secret"
	"attendance/backend/internal/settings"
	"attendance/backend/internal/storage"
	"attendance/backend/internal/telegram"

	"github.com/redis/go-redis/v9"
)

func main() {
	slog.SetDefault(slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelInfo})))
	cmd := "serve"
	if len(os.Args) > 1 {
		cmd = os.Args[1]
	}
	var err error
	switch cmd {
	case "serve":
		err = serve()
	case "migrate":
		err = migrate()
	case "create-admin":
		err = createAdmin(os.Args[2:])
	case "seed-demo":
		err = seedDemo(os.Args[2:])
	default:
		err = fmt.Errorf("unknown command %q (serve | migrate | create-admin | seed-demo)", cmd)
	}
	if err != nil {
		slog.Error("fatal", "err", err)
		os.Exit(1)
	}
}

func migrate() error {
	cfg, err := config.Load()
	if err != nil {
		return err
	}
	res, err := database.Migrate(context.Background(), cfg.DatabaseURL)
	if err != nil {
		return err
	}
	if res.Created {
		fmt.Printf("Created database %s.\n", res.Database)
	}
	if len(res.Applied) == 0 {
		fmt.Printf("Database %s is up to date — all %d migrations already applied (latest: %s).\n", res.Database, res.Total, res.Latest)
	} else {
		fmt.Printf("Database %s: applied %d migration(s): %s. Now at %s.\n", res.Database, len(res.Applied), strings.Join(res.Applied, ", "), res.Latest)
	}
	return nil
}

func serve() error {
	cfg, err := config.Load()
	if err != nil {
		return err
	}
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	if _, err := database.Migrate(ctx, cfg.DatabaseURL); err != nil {
		return err
	}
	db, err := database.Open(cfg.DatabaseURL, !cfg.IsProduction())
	if err != nil {
		return err
	}
	ropt, err := redis.ParseURL(cfg.RedisURL)
	if err != nil {
		return fmt.Errorf("invalid REDIS_URL: %w", err)
	}
	rdb := redis.NewClient(ropt)
	defer rdb.Close()

	store, err := storage.NewS3(cfg.StorageEndpoint, cfg.StorageAccessKey, cfg.StorageSecretKey, cfg.StorageBucket, cfg.StorageRegion, cfg.StorageUseSSL, cfg.StorageURLStyle)
	if err != nil {
		return err
	}
	for i := 0; i < 10; i++ {
		if err = store.EnsureBucket(ctx); err == nil {
			break
		}
		slog.Warn("storage not ready, retrying", "err", err)
		time.Sleep(3 * time.Second)
	}
	if err != nil {
		return fmt.Errorf("storage: %w", err)
	}

	box, err := secret.New(cfg.JWTSecret)
	if err != nil {
		return err
	}
	st := settings.NewService(db, box)
	// The bot token is a setting (Settings → Telegram in the admin panel), read on each call.
	tg := telegram.New(func(ctx context.Context) string {
		if s, err := st.Get(ctx); err == nil {
			return s.TelegramBotToken
		}
		return ""
	}, cfg.TelegramAPIBase)
	notifier := notification.New(db, tg, st)
	go notification.SyncBot(ctx, tg, st)
	authSvc := auth.NewService(db, cfg.JWTSecret, cfg.JWTTTL)
	empSvc := employee.NewService(db)
	attSvc := attendance.NewService(db, st, store, notifier)
	leaveSvc := leave.NewService(db, st, notifier)
	repSvc := report.NewService(db, st)

	srv := httpapi.New(cfg, httpapi.Deps{DB: db, Redis: rdb, Store: store, Auth: authSvc, Settings: st, Employees: empSvc,
		Branches: branch.NewService(db), Attendance: attSvc, Leave: leaveSvc, Reports: repSvc, Schedules: schedule.NewService(db, st), Access: access.NewService(db), Telegram: tg}).HTTPServer(cfg.HTTPAddr)
	sched := scheduler.New(db, rdb, st, repSvc, notifier)
	sched.BackupStatusFile = cfg.BackupStatusFile
	go sched.Run(ctx)

	errCh := make(chan error, 1)
	go func() {
		slog.Info("api listening", "addr", cfg.HTTPAddr)
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			errCh <- err
		}
	}()
	select {
	case <-ctx.Done():
	case err := <-errCh:
		return err
	}
	slog.Info("shutting down")
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	return srv.Shutdown(shutdownCtx)
}

// createAdmin bootstraps a super admin. The password is read from ADMIN_PASSWORD
// (not a flag) so it does not end up in shell history or `ps` output.
func createAdmin(args []string) error {
	fs := flag.NewFlagSet("create-admin", flag.ExitOnError)
	code := fs.String("code", "", "employee ID, e.g. GT000")
	name := fs.String("name", "", "full name")
	tgID := fs.Int64("telegram-id", 0, "Telegram user ID for the Mini App (shown when an unregistered user opens it); optional")
	email := fs.String("email", "", "admin panel login email (password from ADMIN_PASSWORD)")
	_ = fs.Parse(args)
	if *code == "" || *name == "" || (*tgID == 0 && strings.TrimSpace(*email) == "") {
		return errors.New("usage: api create-admin -code GT000 -name \"Owner Name\" [-email you@company.com] [-telegram-id 123456789] (at least one of -email / -telegram-id)")
	}
	cfg, err := config.Load()
	if err != nil {
		return err
	}
	ctx := context.Background()
	if _, err := database.Migrate(ctx, cfg.DatabaseURL); err != nil {
		return err
	}
	db, err := database.Open(cfg.DatabaseURL, false)
	if err != nil {
		return err
	}
	cli := &auth.Actor{Employee: models.Employee{RoleID: models.RoleSuperAdmin, FullName: "CLI"}, Channel: auth.ChannelCLI}
	svc := employee.NewService(db)
	in := employee.Input{EmployeeCode: *code, FullName: *name, Role: "super_admin"}
	if *tgID != 0 {
		in.TelegramUserID = tgID
	}
	emp, err := svc.Create(ctx, cli, in)
	if err != nil {
		return err
	}
	fmt.Printf("created super admin %s (%s), employee id %d\n", emp.EmployeeCode, emp.FullName, emp.ID)
	if strings.TrimSpace(*email) != "" {
		pw := os.Getenv("ADMIN_PASSWORD")
		if pw == "" {
			return errors.New("set ADMIN_PASSWORD to create the admin panel login")
		}
		if _, err := svc.CreateUser(ctx, cli, emp.ID, *email, pw); err != nil {
			return err
		}
		fmt.Printf("admin panel login created for %s\n", *email)
	}
	return nil
}

// seedDemo fills the database with sample data for trying out the admin panel.
// Every demo login shares one password: DEMO_PASSWORD, or a default.
func seedDemo(args []string) error {
	fs := flag.NewFlagSet("seed-demo", flag.ExitOnError)
	force := fs.Bool("force", false, "allow seeding when APP_ENV=production")
	_ = fs.Parse(args)
	cfg, err := config.Load()
	if err != nil {
		return err
	}
	if cfg.IsProduction() && !*force {
		return errors.New("APP_ENV=production: refusing to add demo data (use -force on a throwaway database only)")
	}
	pw := os.Getenv("DEMO_PASSWORD")
	if pw == "" {
		pw = "Demo@12345"
	}
	ctx := context.Background()
	if _, err := database.Migrate(ctx, cfg.DatabaseURL); err != nil {
		return err
	}
	db, err := database.Open(cfg.DatabaseURL, false)
	if err != nil {
		return err
	}
	box, err := secret.New(cfg.JWTSecret)
	if err != nil {
		return err
	}
	sum, err := demo.Seed(ctx, demo.Deps{DB: db, Settings: settings.NewService(db, box)}, demo.Options{Password: pw})
	if errors.Is(err, demo.ErrAlreadySeeded) {
		fmt.Println("Demo data is already in this database (employees with codes D-…); nothing changed.")
		return nil
	}
	if err != nil {
		return err
	}
	fmt.Printf("Demo data added: %d branches (%s), %d employees, %d attendance days, %d leave requests, %d new holidays,\n"+
		"%d extra shifts (incl. a night shift), %d roster entries, %d special schedules.\n\n",
		len(sum.Branches), strings.Join(sum.Branches, ", "), sum.Employees, sum.AttendanceDays, sum.LeaveRequests, sum.Holidays,
		sum.Shifts, sum.RosterEntries, sum.SpecialSchedules)
	fmt.Println("Admin panel logins:")
	for _, l := range sum.Logins {
		p := pw
		if l.Existing {
			p = "(your existing password)"
		}
		fmt.Printf("  %-28s %-22s %-34s %s\n", l.Email, p, l.Role, l.Name)
	}
	return nil
}
