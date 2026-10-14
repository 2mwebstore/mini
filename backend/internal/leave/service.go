// Package leave implements leave requests and their approval.
package leave

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
	maxRangeDays    = 60
	maxBackdateDays = 30
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

func (s *Service) Types(ctx context.Context) ([]models.LeaveType, error) {
	var ts []models.LeaveType
	return ts, s.db.WithContext(ctx).Where("active = 1").Order("sort_order").Find(&ts).Error
}

type SubmitInput struct {
	LeaveTypeID uint64 `json:"leave_type_id" binding:"required"`
	StartDate   string `json:"start_date" binding:"required"`
	EndDate     string `json:"end_date" binding:"required"`
	Reason      string `json:"reason" binding:"required,max=1000"`
}

// Preview validates a request without saving it (the Mini App shows working days while the form is filled in).
func (s *Service) Preview(ctx context.Context, actor *auth.Actor, in SubmitInput) (*models.LeaveRequest, error) {
	lr, _, err := s.validate(ctx, s.db, actor, in)
	return lr, err
}

func (s *Service) validate(ctx context.Context, tx *gorm.DB, actor *auth.Actor, in SubmitInput) (*models.LeaveRequest, *models.LeaveType, error) {
	if !actor.Has(auth.PermLeaveSelf) {
		return nil, nil, apperr.ErrForbidden
	}
	var lt models.LeaveType
	if err := tx.WithContext(ctx).Where("id = ? AND active = 1", in.LeaveTypeID).First(&lt).Error; err != nil {
		return nil, nil, apperr.BadRequest("INVALID_LEAVE_TYPE", "Unknown leave type.")
	}
	start, err := timeutil.ParseDate(in.StartDate)
	if err != nil {
		return nil, nil, apperr.BadRequest("INVALID_DATE", err.Error())
	}
	end, err := timeutil.ParseDate(in.EndDate)
	if err != nil {
		return nil, nil, apperr.BadRequest("INVALID_DATE", err.Error())
	}
	if end.Before(start) {
		return nil, nil, apperr.BadRequest("INVALID_RANGE", "End date must be on or after the start date.")
	}
	if end.Sub(start) > maxRangeDays*24*time.Hour {
		return nil, nil, apperr.BadRequest("RANGE_TOO_LONG", fmt.Sprintf("A single request can cover at most %d days.", maxRangeDays))
	}
	st, err := s.settings.Get(ctx)
	if err != nil {
		return nil, nil, err
	}
	today := timeutil.DateOf(s.Now(), st.Location)
	if start.Before(today.AddDate(0, 0, -maxBackdateDays)) {
		return nil, nil, apperr.BadRequest("TOO_FAR_BACK", fmt.Sprintf("Leave can be back-dated at most %d days.", maxBackdateDays))
	}
	reason := strings.TrimSpace(in.Reason)
	if reason == "" {
		return nil, nil, apperr.BadRequest("REASON_REQUIRED", "Please give a reason.")
	}
	isHol, err := attendance.HolidaySet(ctx, tx, start, end)
	if err != nil {
		return nil, nil, err
	}
	days, err := attendance.WorkingDays(ctx, tx, &actor.Employee, start, end, isHol)
	if err != nil {
		return nil, nil, err
	}
	if days == 0 {
		return nil, nil, apperr.BadRequest("NO_WORKING_DAYS", "The selected dates contain no working days.")
	}
	var overlap int64
	tx.WithContext(ctx).Model(&models.LeaveRequest{}).
		Where("employee_id = ? AND status IN ? AND start_date <= ? AND end_date >= ?", actor.ID(), []string{models.LeavePending, models.LeaveApproved}, end, start).
		Count(&overlap)
	if overlap > 0 {
		return nil, nil, apperr.Conflict("OVERLAP", "You already have a pending or approved leave covering these dates.")
	}
	return &models.LeaveRequest{EmployeeID: actor.ID(), LeaveTypeID: lt.ID, StartDate: start, EndDate: end, Days: days,
		Reason: reason, Status: models.LeavePending, LeaveType: &lt}, &lt, nil
}

func (s *Service) Submit(ctx context.Context, actor *auth.Actor, in SubmitInput) (*models.LeaveRequest, error) {
	var out *models.LeaveRequest
	var lt *models.LeaveType
	err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		// Serialize an employee's submissions so the overlap check is reliable.
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Select("id").First(&models.Employee{}, actor.ID()).Error; err != nil {
			return err
		}
		lr, t, err := s.validate(ctx, tx, actor, in)
		if err != nil {
			return err
		}
		lr.LeaveType = nil
		if err := tx.Create(lr).Error; err != nil {
			return err
		}
		if err := audit.Write(ctx, tx, actor, audit.Entry{Action: audit.ActionLeaveSubmit, EntityType: "leave", EntityID: lr.ID, TargetEmployeeID: actor.ID(),
			Details: map[string]any{"type": t.Code, "start": lr.StartDate.Format("2006-01-02"), "end": lr.EndDate.Format("2006-01-02"), "days": lr.Days}}); err != nil {
			return err
		}
		out, lt = lr, t
		return nil
	})
	if err != nil {
		return nil, err
	}
	out.LeaveType = lt
	s.notify.LeaveSubmitted(*out, actor.Employee, lt.Name)
	return out, nil
}

func (s *Service) Cancel(ctx context.Context, actor *auth.Actor, id uint64) (*models.LeaveRequest, error) {
	var lr models.LeaveRequest
	err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).First(&lr, id).Error; err != nil || lr.EmployeeID != actor.ID() {
			return apperr.NotFound("NOT_FOUND", "Leave request not found.")
		}
		if lr.Status != models.LeavePending {
			return apperr.Conflict("NOT_PENDING", "Only pending requests can be cancelled.")
		}
		lr.Status = models.LeaveCancelled
		if err := tx.Model(&lr).Update("status", lr.Status).Error; err != nil {
			return err
		}
		return audit.Write(ctx, tx, actor, audit.Entry{Action: audit.ActionLeaveCancel, EntityType: "leave", EntityID: lr.ID, TargetEmployeeID: actor.ID()})
	})
	return &lr, err
}

func (s *Service) Review(ctx context.Context, actor *auth.Actor, id uint64, approve bool, note string) (*models.LeaveRequest, error) {
	if !actor.IsReviewer() {
		return nil, apperr.ErrForbidden
	}
	note = strings.TrimSpace(note)
	if len(note) > 500 {
		return nil, apperr.BadRequest("NOTE_TOO_LONG", "Note must be 500 characters or fewer.")
	}
	now := s.Now()
	var lr models.LeaveRequest
	err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Preload("Employee").Preload("Employee.WorkSchedule").Preload("LeaveType").
			First(&lr, id).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return apperr.NotFound("NOT_FOUND", "Leave request not found.")
			}
			return err
		}
		if !actor.CanReview(lr.Employee) {
			return apperr.ErrForbidden
		}
		if lr.Status != models.LeavePending {
			return apperr.Conflict("ALREADY_REVIEWED", "This request is already "+strings.ToLower(lr.Status)+".")
		}
		reviewer := actor.ID()
		lr.Status = models.LeaveRejected
		if approve {
			lr.Status = models.LeaveApproved
		}
		lr.ReviewedBy, lr.ReviewedAt, lr.ReviewNote = &reviewer, &now, note
		if err := tx.Model(&models.LeaveRequest{}).Where("id = ?", lr.ID).Updates(map[string]any{
			"status": lr.Status, "reviewed_by": reviewer, "reviewed_at": now, "review_note": note}).Error; err != nil {
			return err
		}
		if approve {
			isHol, err := attendance.HolidaySet(ctx, tx, lr.StartDate, lr.EndDate)
			if err != nil {
				return err
			}
			if err := attendance.MarkLeaveDays(ctx, tx, lr.Employee, lr.StartDate, lr.EndDate, isHol, fmt.Sprintf("Leave #%d (%s)", lr.ID, lr.LeaveType.Name)); err != nil {
				return err
			}
		}
		action := audit.ActionLeaveReject
		if approve {
			action = audit.ActionLeaveApprove
		}
		return audit.Write(ctx, tx, actor, audit.Entry{Action: action, EntityType: "leave", EntityID: lr.ID, TargetEmployeeID: lr.EmployeeID,
			Details: map[string]any{"note": note, "start": lr.StartDate.Format("2006-01-02"), "end": lr.EndDate.Format("2006-01-02")}})
	})
	if err != nil {
		return nil, err
	}
	s.notify.LeaveReviewed(lr)
	return &lr, nil
}

func (s *Service) Get(ctx context.Context, actor *auth.Actor, id uint64) (*models.LeaveRequest, error) {
	var lr models.LeaveRequest
	if err := s.db.WithContext(ctx).Preload("Employee").Preload("LeaveType").Preload("Reviewer").First(&lr, id).Error; err != nil {
		return nil, apperr.NotFound("NOT_FOUND", "Leave request not found.")
	}
	if !actor.CanView(lr.Employee) {
		return nil, apperr.NotFound("NOT_FOUND", "Leave request not found.")
	}
	return &lr, nil
}

type ListFilter struct {
	EmployeeID uint64 `form:"employee_id"`
	BranchID   uint64 `form:"branch_id"`
	Status     string `form:"status"`
	From       string `form:"from"`
	To         string `form:"to"`
	Limit      int    `form:"limit"`
}

func (s *Service) List(ctx context.Context, actor *auth.Actor, f ListFilter) ([]models.LeaveRequest, error) {
	scope, err := auth.Scope(ctx, s.db, actor)
	if err != nil {
		return nil, err
	}
	q := s.db.WithContext(ctx).Preload("Employee").Preload("Employee.Branch").Preload("LeaveType").Preload("Reviewer").
		Order("leave_requests.start_date DESC, leave_requests.id DESC")
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
	if f.From != "" {
		if d, err := timeutil.ParseDate(f.From); err == nil {
			q = q.Where("end_date >= ?", d)
		}
	}
	if f.To != "" {
		if d, err := timeutil.ParseDate(f.To); err == nil {
			q = q.Where("start_date <= ?", d)
		}
	}
	if f.Limit <= 0 || f.Limit > 500 {
		f.Limit = 100
	}
	out := []models.LeaveRequest{}
	return out, q.Limit(f.Limit).Find(&out).Error
}

// PendingForReviewer lists pending requests the actor can approve.
func (s *Service) PendingForReviewer(ctx context.Context, actor *auth.Actor) ([]models.LeaveRequest, error) {
	if !actor.IsReviewer() {
		return nil, apperr.ErrForbidden
	}
	q := s.db.WithContext(ctx).Preload("Employee").Preload("Employee.Branch").Preload("LeaveType").
		Joins("JOIN employees e ON e.id = leave_requests.employee_id").
		Where("leave_requests.status = ?", models.LeavePending).Order("leave_requests.id ASC").Limit(50)
	q = auth.ReviewScope(q, actor)
	out := []models.LeaveRequest{}
	return out, q.Find(&out).Error
}
