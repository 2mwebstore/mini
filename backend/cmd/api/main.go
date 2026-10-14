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
	"text/tabwriter"
	"time"

	"attendance/backend/internal/access"
	"attendance/backend/internal/attendance"
	"attendance/backend/internal/auth"
	"attendance/backend/internal/bots"
	"attendance/backend/internal/branch"
	"attendance/backend/internal/config"
	"attendance/backend/internal/database"
	"attendance/backend/internal/demo"
	"attendance/backend/internal/employee"
	"attendance/backend/internal/httpapi"
	"attendance/backend/internal/leave"
	"attendance/backend/internal/models"
	"attendance/backend/internal/notification"
	"attendance/backend/internal/overtime"
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
	case "clear-data":
		err = clearData(os.Args[2:])
	default:
		err = fmt.Errorf("unknown command %q (serve | migrate | create-admin | seed-demo | clear-data)", cmd)
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
	// Each branch has its own bot (set on the branch page); calls pick the branch's token.
	botReg := bots.New(db, box)
	tg := telegram.New(func(context.Context) string { return "" }, cfg.TelegramAPIBase)
	notifier := notification.New(db, tg, botReg)
	go notification.SyncBots(ctx, db, tg, botReg)
	authSvc := auth.NewService(db, cfg.JWTSecret, cfg.JWTTTL)
	empSvc := employee.NewService(db)
	attSvc := attendance.NewService(db, st, store, notifier)
	leaveSvc := leave.NewService(db, st, notifier)
	otSvc := overtime.NewService(db, st, notifier)
	repSvc := report.NewService(db, st)

	srv := httpapi.New(cfg, httpapi.Deps{DB: db, Redis: rdb, Store: store, Auth: authSvc, Settings: st, Employees: empSvc,
		Branches: branch.NewService(db), Attendance: attSvc, Leave: leaveSvc, Overtime: otSvc, Reports: repSvc, Schedules: schedule.NewService(db, st), Access: access.NewService(db), Telegram: tg, Bots: botReg}).HTTPServer(cfg.HTTPAddr)
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

// createAdmin bootstraps the owner: a system user (admin panel account) with the
// super admin role. The password is read from ADMIN_PASSWORD (not a flag) so it
// does not end up in shell history or `ps` output.
func createAdmin(args []string) error {
	fs := flag.NewFlagSet("create-admin", flag.ExitOnError)
	_ = fs.String("code", "", "ignored (system users get an SU-… code)")
	name := fs.String("name", "", "full name")
	tgID := fs.Int64("telegram-id", 0, "Telegram user ID, to get review notifications and use the Mini App's Team tab; optional")
	email := fs.String("email", "", "admin panel login email (password from ADMIN_PASSWORD)")
	_ = fs.Parse(args)
	if *name == "" || strings.TrimSpace(*email) == "" {
		return errors.New("usage: ADMIN_PASSWORD=… api create-admin -name \"Owner Name\" -email you@company.com [-telegram-id 123456789]")
	}
	pw := os.Getenv("ADMIN_PASSWORD")
	if pw == "" {
		return errors.New("set ADMIN_PASSWORD to the owner's admin panel password")
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
	role := "super_admin"
	in := access.UserInput{FullName: name, Email: email, Password: &pw, Role: &role}
	if *tgID != 0 {
		in.TelegramUserID = tgID
	}
	u, err := access.NewService(db).CreateUser(ctx, cli, in)
	if err != nil {
		return err
	}
	fmt.Printf("created owner %s <%s> (system user %d)\n", u.FullName, u.Email, u.ID)
	return nil
}

// clearData wipes all business data (keeping the owner, roles and company settings)
// so the system can be set up from scratch — e.g. before a client demo.
func clearData(args []string) error {
	fs := flag.NewFlagSet("clear-data", flag.ExitOnError)
	yes := fs.Bool("yes", false, "confirm: delete every branch, employee, system user (but the owner), attendance and leave record")
	force := fs.Bool("force", false, "allow it when APP_ENV=production")
	_ = fs.Parse(args)
	if !*yes {
		return errors.New("this deletes all branches, employees, system users (the owner stays), attendance, leave and schedules: run it again with -yes")
	}
	cfg, err := config.Load()
	if err != nil {
		return err
	}
	if cfg.IsProduction() && !*force {
		return errors.New("APP_ENV=production: refusing to clear the data (use -force on a throwaway database only)")
	}
	ctx := context.Background()
	if _, err := database.Migrate(ctx, cfg.DatabaseURL); err != nil {
		return err
	}
	db, err := database.Open(cfg.DatabaseURL, false)
	if err != nil {
		return err
	}
	out, err := demo.Clear(ctx, db)
	if err != nil {
		return err
	}
	fmt.Printf("Cleared: %d branches, %d employees, %d system users, %d attendance days, %d leave requests.\n",
		out.Branches, out.Employees, out.SystemUsers, out.Attendance, out.LeaveRequests)
	if len(out.Owners) > 0 {
		fmt.Println("Kept the owner:", strings.Join(out.Owners, ", "))
	} else {
		fmt.Println("No owner was found: create one with `api create-admin`, or run `api seed-demo` for a demo owner.")
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
	fmt.Printf("Demo data added: %d branches (%s), %d employees, %d attendance days, %d leave requests, %d overtime requests,\n"+
		"%d new holidays, %d extra shifts (incl. a night shift), %d roster entries, %d special schedules.\n\n",
		len(sum.Branches), strings.Join(sum.Branches, ", "), sum.Employees, sum.AttendanceDays, sum.LeaveRequests, sum.Overtime, sum.Holidays,
		sum.Shifts, sum.RosterEntries, sum.SpecialSchedules)
	fmt.Println("System users (admin panel logins):")
	tw := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	for _, l := range sum.Logins {
		p := pw
		if l.Existing {
			p = "(your existing password)"
		}
		fmt.Fprintf(tw, "  %s\t%s\t%s\t%s\n", l.Email, p, l.Role, l.Name)
	}
	tw.Flush()
	return nil
}
