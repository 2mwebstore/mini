// Package overtime implements overtime requests: an employee asks to work extra
// time on a date (by default from the end of their shift, for an hour), one of
// their reviewers approves or rejects it, and reports count it.
package overtime

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"attendance/backend/internal/apperr"
	"attendance/backend/internal/attendance"
	"attendance/backend/internal/audit"
	"attendance/backend/internal/auth"
	"attendance/backend/internal/models"
	"attendance/backend/internal/notification"
	"attendance/backend/internal/settings"
	"attendance/backend/internal/timeutil"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

const (
	minMinutes      = 15
	maxMinutes      = 12 * 60
	maxBackdateDays = 30
	maxAheadDays    = 60
	defaultMinutes  = 60 // the suggested length: an hour after the shift
)

type Service struct {
	db       *gorm.DB
	settings *settings.Service
	notify   *notification.Notifier
	Now      func() time.Time
}

func NewService(db *gorm.DB, st *settings.Service, n *notification.Notifier) *Service {
	return &Service{db: db, settings: st, notify: n, Now: func() time.Time { return time.Now().UTC() }}
}

// Plan is what the form starts from for a date: the person's shift that day and the
// suggested overtime — from the shift's end, for an hour.
type Plan struct {
	Date        string `json:"date"`
	Work        bool   `json:"work"` // a work day (else the usual shift's hours are used)
	ShiftName   string `json:"shift_name"`
	ShiftStart  string `json:"shift_start"`
	ShiftEnd    string `json:"shift_end"`
	CrossDay    bool   `json:"cross_day"` // the shift ends the next day
	DefaultFrom string `json:"default_from"`
	DefaultTo   string `json:"default_to"`
}

// Plan returns the shift and the suggested overtime of the actor on date.
func (s *Service) Plan(ctx context.Context, actor *auth.Actor, date string) (*Plan, error) {
	d, err := timeutil.ParseDate(date)
	if err != nil {
		return nil, apperr.BadRequest("INVALID_DATE", err.Error())
	}
	day, _, err := s.dayOf(ctx, s.db, &actor.Employee, d)
	if err != nil {
		return nil, err
	}
	p := &Plan{Date: d.Format("2006-01-02"), Work: day.Work, ShiftName: day.ShiftName, ShiftStart: day.Start, ShiftEnd: day.End, CrossDay: day.CrossDay}
	p.DefaultFrom = day.End
	if t, err := time.Parse("15:04", day.End); err == nil {
		p.DefaultTo = t.Add(defaultMinutes * time.Minute).Format("15:04")
	}
	return p, nil
}

// dayOf: the person's shift on d — the planned one, or on a day off their usual
// shift (whose end is still where overtime starts by default).
func (s *Service) dayOf(ctx context.Context, db *gorm.DB, emp *models.Employee, d time.Time) (attendance.Day, *time.Location, error) {
	st, err := s.settings.Get(ctx)
	if err != nil {
		return attendance.Day{}, nil, err
	}
	planner, err := attendance.LoadPlanner(ctx, db, []uint64{emp.ID}, d, d)
	if err != nil {
		return attendance.Day{}, nil, err
	}
	day := planner.Day(emp, d)
	if !day.Work {
		usual := attendance.ShiftDay(planner.Usual(emp))
		usual.Work = false
		day = usual
	}
	return day, st.Location, nil
}

type SubmitInput struct {
	Date      string `json:"date" binding:"required"`       // YYYY-MM-DD or DD-MM-YYYY
	StartTime string `json:"start_time" binding:"required"` // HH:MM company time
	EndTime   string `json:"end_time" binding:"required"`   // HH:MM; earlier than the start = the next day
	Reason    string `json:"reason"`
}

// Submit records the actor's overtime request (PENDING) and tells their reviewers.
func (s *Service) Submit(ctx context.Context, actor *auth.Actor, in SubmitInput) (*models.OvertimeRequest, error) {
	if !actor.Has(auth.PermAttendanceSelf) {
		return nil, apperr.Forbidden("Only employees request overtime.")
	}
	reason := strings.TrimSpace(in.Reason)
	if reason == "" {
		return nil, apperr.BadRequest("REASON_REQUIRED", "Say what the overtime is for.")
	}
	if len([]rune(reason)) > 500 {
		return nil, apperr.BadRequest("REASON_TOO_LONG", "Reason: at most 500 characters.")
	}
	d, err := timeutil.ParseDate(in.Date)
	if err != nil {
		return nil, apperr.BadRequest("INVALID_DATE", err.Error())
	}
	var ot models.OvertimeRequest
	var loc *time.Location
	err = s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		// One submission at a time per person, so the overlap check holds.
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Select("id").First(&models.Employee{}, actor.ID()).Error; err != nil {
			return err
		}
		day, l, err := s.dayOf(ctx, tx, &actor.Employee, d)
		if err != nil {
			return err
		}
		loc = l
		today := timeutil.DateOf(s.Now(), loc)
		if d.Before(today.AddDate(0, 0, -maxBackdateDays)) {
			return apperr.BadRequest("TOO_FAR_BACK", fmt.Sprintf("Overtime can be asked for at most %d days back.", maxBackdateDays))
		}
		if d.After(today.AddDate(0, 0, maxAheadDays)) {
			return apperr.BadRequest("TOO_FAR_AHEAD", fmt.Sprintf("Overtime can be asked for at most %d days ahead.", maxAheadDays))
		}
		start, end, err := span(d, day, in.StartTime, in.EndTime, loc)
		if err != nil {
			return err
		}
		mins := int(end.Sub(start).Minutes())
		if mins < minMinutes || mins > maxMinutes {
			return apperr.BadRequest("INVALID_LENGTH", fmt.Sprintf("Overtime is %d minutes to %d hours.", minMinutes, maxMinutes/60))
		}
		if day.Work { // overtime is time outside the shift
			ss, err1 := day.StartAt(d, loc)
			se, err2 := day.EndAt(d, loc)
			if err1 == nil && err2 == nil && start.Before(se) && end.After(ss) {
				return apperr.BadRequest("OVERLAPS_SHIFT", fmt.Sprintf("Overtime is time outside your shift (%s–%s).", day.Start, day.End))
			}
		}
		var n int64
		tx.Model(&models.OvertimeRequest{}).Where("employee_id = ? AND status IN ? AND start_at < ? AND end_at > ?",
			actor.ID(), []string{models.LeavePending, models.LeaveApproved}, end, start).Count(&n)
		if n > 0 {
			return apperr.Conflict("OVERLAP", "You already asked for overtime at that time.")
		}
		ot = models.OvertimeRequest{EmployeeID: actor.ID(), WorkDate: d, StartAt: start.UTC(), EndAt: end.UTC(), Minutes: mins,
			Reason: reason, Status: models.LeavePending}
		if err := tx.Create(&ot).Error; err != nil {
			return err
		}
		return audit.Write(ctx, tx, actor, audit.Entry{Action: audit.ActionOvertimeSubmit, EntityType: "overtime", EntityID: ot.ID, TargetEmployeeID: actor.ID(),
			Details: map[string]any{"date": d.Format("2006-01-02"), "from": in.StartTime, "to": in.EndTime, "minutes": mins}})
	})
	if err != nil {
		return nil, err
	}
	s.notify.OvertimeSubmitted(ot, actor.Employee, loc)
	return &ot, nil
}

// span turns a date and HH:MM times into instants. The start is on date — or on the
// next morning when it falls in the tail of a night shift; an end at or before the
// start is on the following day.
func span(d time.Time, day attendance.Day, from, to string, loc *time.Location) (time.Time, time.Time, error) {
	if _, err := time.Parse("15:04", from); err != nil {
		return time.Time{}, time.Time{}, apperr.BadRequest("INVALID_TIME", "From: a time like 17:30.")
	}
	if _, err := time.Parse("15:04", to); err != nil {
		return time.Time{}, time.Time{}, apperr.BadRequest("INVALID_TIME", "To: a time like 18:30.")
	}
	startDay := d
	if day.CrossDay && from <= day.End {
		startDay = d.AddDate(0, 0, 1)
	}
	start, err := timeutil.At(startDay, from, loc)
	if err != nil {
		return time.Time{}, time.Time{}, apperr.BadRequest("INVALID_TIME", err.Error())
	}
	endDay := startDay
	if to <= from {
		endDay = startDay.AddDate(0, 0, 1)
	}
	end, err := timeutil.At(endDay, to, loc)
	if err != nil {
		return time.Time{}, time.Time{}, apperr.BadRequest("INVALID_TIME", err.Error())
	}
	return start, end, nil
}

// Cancel: the employee withdraws their request — while pending, or approved and not started.
func (s *Service) Cancel(ctx context.Context, actor *auth.Actor, id uint64) (*models.OvertimeRequest, error) {
	var ot models.OvertimeRequest
	err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).First(&ot, id).Error; err != nil || ot.EmployeeID != actor.ID() {
			return apperr.NotFound("NOT_FOUND", "Overtime request not found.")
		}
		switch {
		case ot.Status == models.LeavePending:
		case ot.Status == models.LeaveApproved && ot.StartAt.After(s.Now()):
		default:
			return apperr.Conflict("NOT_CANCELLABLE", "Only pending overtime, or approved overtime that hasn't started, can be cancelled.")
		}
		was := ot.Status
		ot.Status = models.LeaveCancelled
		if err := tx.Model(&ot).Update("status", ot.Status).Error; err != nil {
			return err
		}
		return audit.Write(ctx, tx, actor, audit.Entry{Action: audit.ActionOvertimeCancel, EntityType: "overtime", EntityID: ot.ID, TargetEmployeeID: actor.ID(),
			Details: map[string]any{"was": was}})
	})
	return &ot, err
}

// Review approves or rejects a pending request; only someone who reviews the person may.
func (s *Service) Review(ctx context.Context, actor *auth.Actor, id uint64, approve bool, note string) (*models.OvertimeRequest, error) {
	if !actor.IsReviewer() {
		return nil, apperr.ErrForbidden
	}
	note = strings.TrimSpace(note)
	if len([]rune(note)) > 500 {
		return nil, apperr.BadRequest("NOTE_TOO_LONG", "Note must be 500 characters or fewer.")
	}
	st, err := s.settings.Get(ctx)
	if err != nil {
		return nil, err
	}
	now := s.Now()
	var ot models.OvertimeRequest
	err = s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Preload("Employee").First(&ot, id).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return apperr.NotFound("NOT_FOUND", "Overtime request not found.")
			}
			return err
		}
		if !actor.CanReview(ot.Employee) {
			return apperr.ErrForbidden
		}
		if ot.Status != models.LeavePending {
			return apperr.Conflict("ALREADY_REVIEWED", "This request is already "+strings.ToLower(ot.Status)+".")
		}
		reviewer := actor.ID()
		ot.Status = models.LeaveRejected
		if approve {
			ot.Status = models.LeaveApproved
		}
		ot.ReviewedBy, ot.ReviewedAt, ot.ReviewNote = &reviewer, &now, note
		if err := tx.Model(&models.OvertimeRequest{}).Where("id = ?", ot.ID).Updates(map[string]any{
			"status": ot.Status, "reviewed_by": reviewer, "reviewed_at": now, "review_note": note}).Error; err != nil {
			return err
		}
		action := audit.ActionOvertimeReject
		if approve {
			action = audit.ActionOvertimeApprove
		}
		return audit.Write(ctx, tx, actor, audit.Entry{Action: action, EntityType: "overtime", EntityID: ot.ID, TargetEmployeeID: ot.EmployeeID,
			Details: map[string]any{"note": note, "date": ot.WorkDate.Format("2006-01-02"), "minutes": ot.Minutes}})
	})
	if err != nil {
		return nil, err
	}
	s.notify.OvertimeReviewed(ot, st.Location)
	return &ot, nil
}

func (s *Service) Get(ctx context.Context, actor *auth.Actor, id uint64) (*models.OvertimeRequest, error) {
	var ot models.OvertimeRequest
	if err := s.db.WithContext(ctx).Preload("Employee").Preload("Employee.Branch").Preload("Reviewer").First(&ot, id).Error; err != nil {
		return nil, apperr.NotFound("NOT_FOUND", "Overtime request not found.")
	}
	if !actor.CanView(ot.Employee) {
		return nil, apperr.NotFound("NOT_FOUND", "Overtime request not found.")
	}
	return &ot, nil
}

type ListFilter struct {
	EmployeeID uint64 `form:"employee_id"`
	BranchID   uint64 `form:"branch_id"`
	Status     string `form:"status"`
	From       string `form:"from"`
	To         string `form:"to"`
}

// Page lists the requests the actor may see (newest date first) and how many there are.
func (s *Service) Page(ctx context.Context, actor *auth.Actor, f ListFilter, page, size int) ([]models.OvertimeRequest, int64, error) {
	scope, err := auth.Scope(ctx, s.db, actor)
	if err != nil {
		return nil, 0, err
	}
	q := s.db.WithContext(ctx).Model(&models.OvertimeRequest{})
	if scope != nil {
		q = q.Where("employee_id IN ?", scope)
	}
	if f.EmployeeID != 0 {
		q = q.Where("employee_id = ?", f.EmployeeID)
	}
	if f.BranchID != 0 {
		q = q.Where("employee_id IN (?)", s.db.Model(&models.Employee{}).Select("id").Where("branch_id = ?", f.BranchID))
	}
	if f.Status != "" {
		q = q.Where("status = ?", strings.ToUpper(f.Status))
	}
	if d, err := timeutil.ParseDate(f.From); err == nil && f.From != "" {
		q = q.Where("work_date >= ?", d)
	}
	if d, err := timeutil.ParseDate(f.To); err == nil && f.To != "" {
		q = q.Where("work_date <= ?", d)
	}
	var total int64
	if err := q.Session(&gorm.Session{}).Count(&total).Error; err != nil {
		return nil, 0, err
	}
	if size <= 0 {
		size = 1000
	}
	out := []models.OvertimeRequest{}
	err = q.Preload("Employee").Preload("Employee.Branch").Preload("Reviewer").
		Order("work_date DESC, start_at DESC, id DESC").Offset((page - 1) * size).Limit(size).Find(&out).Error
	return out, total, err
}

// Mine: the actor's own requests, newest first.
func (s *Service) Mine(ctx context.Context, actor *auth.Actor) ([]models.OvertimeRequest, error) {
	out := []models.OvertimeRequest{}
	return out, s.db.WithContext(ctx).Preload("Reviewer").Where("employee_id = ?", actor.ID()).
		Order("work_date DESC, start_at DESC").Limit(100).Find(&out).Error
}

// PendingForReviewer: requests waiting for the actor's decision.
func (s *Service) PendingForReviewer(ctx context.Context, actor *auth.Actor) ([]models.OvertimeRequest, error) {
	if !actor.IsReviewer() {
		return nil, apperr.ErrForbidden
	}
	q := s.db.WithContext(ctx).Preload("Employee").Preload("Employee.Branch").
		Joins("JOIN employees e ON e.id = overtime_requests.employee_id").
		Where("overtime_requests.status = ?", models.LeavePending).Order("overtime_requests.start_at ASC").Limit(100)
	q = auth.ReviewScope(q, actor)
	out := []models.OvertimeRequest{}
	return out, q.Find(&out).Error
}
