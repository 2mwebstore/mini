// Package scheduler runs the daily jobs: missing check-in alert, automated daily
// report and end-of-day close (marking absences). Times come from company
// settings and can change without a restart. A Redis lock makes each job run
// once per day even with several API replicas, and missed runs are caught up
// for a short window after a restart.
package scheduler

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"os"
	"strings"
	"time"

	"attendance/backend/internal/audit"
	"attendance/backend/internal/auth"
	"attendance/backend/internal/models"
	"attendance/backend/internal/notification"
	"attendance/backend/internal/report"
	"attendance/backend/internal/settings"
	"attendance/backend/internal/telegram"
	"attendance/backend/internal/timeutil"

	"github.com/redis/go-redis/v9"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

const catchUpWindow = 2 * time.Hour

type Scheduler struct {
	db       *gorm.DB
	rdb      *redis.Client
	settings *settings.Service
	reports  *report.Service
	notify   *notification.Notifier
	Now      func() time.Time
	// BackupStatusFile is written by the backup container after each run; when it
	// exists, a failed or missing nightly backup is reported to the owner.
	BackupStatusFile string
}

func New(db *gorm.DB, rdb *redis.Client, st *settings.Service, rep *report.Service, n *notification.Notifier) *Scheduler {
	return &Scheduler{db: db, rdb: rdb, settings: st, reports: rep, notify: n, Now: func() time.Time { return time.Now().UTC() }}
}

// SystemActor sees everything; used for scheduled reports.
func SystemActor() *auth.Actor {
	return &auth.Actor{Employee: models.Employee{RoleID: models.RoleSuperAdmin, FullName: "System"}, Channel: auth.ChannelSystem}
}

func (s *Scheduler) Run(ctx context.Context) {
	t := time.NewTicker(time.Minute)
	defer t.Stop()
	s.tick(ctx)
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			s.tick(ctx)
		}
	}
}

type job struct {
	name    string
	at      string
	enabled bool
	fn      func(ctx context.Context, date time.Time, st *settings.Settings) error
}

func (s *Scheduler) tick(ctx context.Context) {
	st, err := s.settings.Get(ctx)
	if err != nil {
		slog.Error("scheduler: settings", "err", err)
		return
	}
	now := s.Now()
	today := timeutil.DateOf(now, st.Location)
	jobs := []job{
		{"missing_checkin_alert", st.MissingCheckinAlertTime, true, s.MissingCheckinAlert},
		{"daily_report", st.DailyReportTime, st.DailyReportEnabled, s.DailyReport},
		{"day_close", st.DayCloseTime, true, s.CloseDay},
		{"backup_check", "08:00", s.BackupStatusFile != "", s.BackupCheck},
	}
	for _, j := range jobs {
		if !j.enabled {
			continue
		}
		due, err := timeutil.At(today, j.at, st.Location)
		if err != nil || now.Before(due) || now.Sub(due) > catchUpWindow {
			continue
		}
		lock := fmt.Sprintf("job:%s:%s", j.name, today.Format("2006-01-02"))
		ok, err := s.rdb.SetNX(ctx, lock, now.Format(time.RFC3339), 26*time.Hour).Result()
		if err != nil || !ok {
			continue
		}
		slog.Info("scheduler: running job", "job", j.name, "date", today.Format("2006-01-02"))
		if err := j.fn(ctx, today, st); err != nil {
			slog.Error("scheduler: job failed", "job", j.name, "err", err)
			s.rdb.Del(ctx, lock) // allow retry on the next tick
		}
	}
}

func (s *Scheduler) dayReport(ctx context.Context, date time.Time) (*report.Report, error) {
	return s.reports.Build(ctx, SystemActor(), report.Filter{From: date, To: date})
}

func (s *Scheduler) activeBranches(ctx context.Context) ([]models.Branch, error) {
	var bs []models.Branch
	return bs, s.db.WithContext(ctx).Where("status = ?", models.EmployeeActive).Order("name").Find(&bs).Error
}

// MissingCheckinAlert tells reviewers who has not checked in by the alert time:
// each branch's admin (or review chat) gets their branch, the company admins everyone.
func (s *Scheduler) MissingCheckinAlert(ctx context.Context, date time.Time, st *settings.Settings) error {
	rep, err := s.dayReport(ctx, date)
	if err != nil {
		return err
	}
	branches, err := s.activeBranches(ctx)
	if err != nil {
		return err
	}
	var all []string
	byBranch := map[uint64][]string{}
	now, loc := s.Now(), s.settings.Location(ctx)
	for _, r := range rep.Rows {
		if r.Status != report.DayNotYet || !shiftDue(r, date, now, loc) {
			continue
		}
		line := fmt.Sprintf("👤 %s (%s)", telegram.Esc(r.FullName), telegram.Esc(r.EmployeeCode))
		byBranch[r.BranchID] = append(byBranch[r.BranchID], line)
		if len(branches) > 1 && r.Branch != "" {
			line += " · " + telegram.Esc(r.Branch)
		}
		all = append(all, line)
	}
	if len(all) == 0 {
		return nil
	}
	alert := func(title string, lines []string) string {
		return fmt.Sprintf("⚠️ <b>ATTENDANCE ALERT%s</b>\n📅 %s\n❌ No check-in recorded by %s:\n%s\n\nStatus: Absent / Pending Review",
			title, timeutil.FormatDate(date), st.MissingCheckinAlertTime, strings.Join(lines, "\n"))
	}
	for _, b := range branches {
		if lines := byBranch[b.ID]; len(lines) > 0 {
			s.notify.BranchAdmins(b, "alert.missing_checkin", alert(" — "+telegram.Esc(b.Name), lines))
		}
	}
	s.notify.Admins("alert.missing_checkin", alert("", all))
	return nil
}

// DailyReport posts each branch's report to that branch's group (through the
// branch's bot), if it has one.
func (s *Scheduler) DailyReport(ctx context.Context, date time.Time, _ *settings.Settings) error {
	branches, err := s.activeBranches(ctx)
	if err != nil {
		return err
	}
	for _, b := range branches {
		if b.TelegramGroupID == 0 {
			continue
		}
		brep, err := s.reports.Build(ctx, SystemActor(), report.Filter{From: date, To: date, BranchID: b.ID})
		if err != nil {
			return err
		}
		if len(brep.Rows) > 0 {
			s.notify.BranchGroup(b, "report.daily.branch", report.DayText(brep, "DAILY ATTENDANCE REPORT — "+strings.ToUpper(b.Name), true))
		}
	}
	return nil
}

// BackupStatus is what ops/backup/backup.sh writes after each run.
type BackupStatus struct {
	OK   bool      `json:"ok"`
	At   time.Time `json:"at"`
	File string    `json:"file,omitempty"`
	Size string    `json:"size,omitempty"`
}

// ReadBackupStatus returns nil (no error) when no backup has run yet / backups aren't mounted.
func ReadBackupStatus(path string) (*BackupStatus, error) {
	if path == "" {
		return nil, nil
	}
	data, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var bs BackupStatus
	if err := json.Unmarshal(data, &bs); err != nil {
		return nil, fmt.Errorf("backup status: %w", err)
	}
	return &bs, nil
}

// BackupCheck warns the company admins when the last backup failed or is over a day old.
func (s *Scheduler) BackupCheck(ctx context.Context, _ time.Time, st *settings.Settings) error {
	bs, err := ReadBackupStatus(s.BackupStatusFile)
	if err != nil || bs == nil {
		return err
	}
	switch {
	case !bs.OK:
		s.notify.Admins("alert.backup", fmt.Sprintf("❌ <b>Database backup FAILED</b> at %s.\nCheck: <code>docker compose logs backup</code>",
			bs.At.In(st.Location).Format("02 Jan 2006 15:04")))
	case s.Now().Sub(bs.At) > 30*time.Hour:
		s.notify.Admins("alert.backup", fmt.Sprintf("⚠️ <b>No database backup since %s.</b>\nCheck: <code>docker compose logs backup</code>",
			bs.At.In(st.Location).Format("02 Jan 2006 15:04")))
	}
	return nil
}

// CloseDay writes ABSENT records for scheduled employees with no check-in and no
// approved leave, so history is explicit even if schedules change later.
// shiftDue: the row's shift has started and its grace period is over by now.
func shiftDue(r report.DayRow, date, now time.Time, loc *time.Location) bool {
	if r.ShiftStart == "" {
		return true
	}
	start, err := timeutil.At(date, r.ShiftStart, loc)
	return err != nil || !now.Before(start.Add(time.Duration(r.ShiftGrace)*time.Minute))
}

func (s *Scheduler) CloseDay(ctx context.Context, date time.Time, _ *settings.Settings) error {
	rep, err := s.dayReport(ctx, date)
	if err != nil {
		return err
	}
	var created []uint64
	now, loc := s.Now(), s.settings.Location(ctx)
	err = s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		for _, r := range rep.Rows {
			if r.AttendanceID != 0 || (r.Status != report.DayNotYet && r.Status != models.StatusAbsent && r.Status != models.StatusLeave) {
				continue
			}
			if r.Status == report.DayNotYet && !shiftDue(r, date, now, loc) {
				continue // e.g. a night shift starting after the day-close time: not absent yet
			}
			status := models.StatusAbsent
			note := "No check-in recorded"
			if r.Status == models.StatusLeave {
				status, note = models.StatusLeave, "Approved leave"
			}
			att := models.Attendance{EmployeeID: r.EmployeeID, WorkDate: date, Status: status,
				ConfirmationStatus: models.ConfConfirmed, Source: models.SourceSystem, Note: note}
			res := tx.Clauses(clause.OnConflict{DoNothing: true}).Omit(clause.Associations).Create(&att)
			if res.Error != nil {
				return res.Error
			}
			if res.RowsAffected > 0 && status == models.StatusAbsent {
				created = append(created, r.EmployeeID)
			}
		}
		if len(created) == 0 {
			return nil
		}
		return audit.Write(ctx, tx, nil, audit.Entry{Action: audit.ActionAutoAbsent, EntityType: "attendance",
			Details: map[string]any{"date": date.Format("2006-01-02"), "employee_ids": created}})
	})
	return err
}
