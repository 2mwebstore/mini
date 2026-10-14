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
	DayType     string `json:"day_type"` // FULL (default) | FIRST_HALF | SECOND_HALF (one date only)
	Reason      string `json:"reason" binding:"required,max=1000"`
}

// DayPlan is the employee's shift on the first requested date — what a half day
// means for them: a normal shift (08:00-17:00) splits into a morning and an
// afternoon, a cross-day shift (22:00-06:00) into 22:00-02:00 and 02:00-06:00.
type DayPlan struct {
	Date       time.Time `json:"date"`
	Work       bool      `json:"work"`
	Holiday    bool      `json:"holiday"`
	ShiftName  string    `json:"shift_name,omitempty"`
	Start      string    `json:"start,omitempty"`
	End        string    `json:"end,omitempty"`
	WorkType   string    `json:"work_type"` // NORMAL | CROSS_DAY
	FirstHalf  [2]string `json:"first_half"`
	SecondHalf [2]string `json:"second_half"`
	SecondNext bool      `json:"second_half_next_day"` // the second half starts after midnight
	CrossDays  bool      `json:"cross_days"`           // some working day in the range is a cross-day shift
}

// Preview validates a request without saving it (the Mini App shows working days,
// the balance and the employee's shift while the form is filled in). The plan is
// returned even when the request is refused, so the form can explain why.
func (s *Service) Preview(ctx context.Context, actor *auth.Actor, in SubmitInput) (*models.LeaveRequest, *Balance, *DayPlan, error) {
	v, err := s.validate(ctx, s.db, actor, in)
	if v == nil {
		return nil, nil, nil, err
	}
	return v.req, v.bal, v.plan, err
}

type validated struct {
	req  *models.LeaveRequest
	lt   *models.LeaveType
	bal  *Balance
	plan *DayPlan
}

func (s *Service) validate(ctx context.Context, tx *gorm.DB, actor *auth.Actor, in SubmitInput) (*validated, error) {
	if !actor.Has(auth.PermLeaveSelf) {
		return nil, apperr.ErrForbidden
	}
	var lt models.LeaveType
	if err := tx.WithContext(ctx).Where("id = ? AND active = 1", in.LeaveTypeID).First(&lt).Error; err != nil {
		return nil, apperr.BadRequest("INVALID_LEAVE_TYPE", "Unknown leave type.")
	}
	dayType := strings.ToUpper(strings.TrimSpace(in.DayType))
	if dayType == "" {
		dayType = models.LeaveFullDay
	}
	if dayType != models.LeaveFullDay && dayType != models.LeaveFirstHalf && dayType != models.LeaveSecondHalf {
		return nil, apperr.BadRequest("INVALID_DAY_TYPE", "Day type must be FULL, FIRST_HALF or SECOND_HALF.")
	}
	half := dayType != models.LeaveFullDay
	start, err := timeutil.ParseDate(in.StartDate)
	if err != nil {
		return nil, apperr.BadRequest("INVALID_DATE", err.Error())
	}
	end, err := timeutil.ParseDate(in.EndDate)
	if err != nil {
		return nil, apperr.BadRequest("INVALID_DATE", err.Error())
	}
	if end.Before(start) {
		return nil, apperr.BadRequest("INVALID_RANGE", "End date must be on or after the start date.")
	}
	if end.Sub(start) > maxRangeDays*24*time.Hour {
		return nil, apperr.BadRequest("RANGE_TOO_LONG", fmt.Sprintf("A single request can cover at most %d days.", maxRangeDays))
	}
	st, err := s.settings.Get(ctx)
	if err != nil {
		return nil, err
	}
	isHol, err := attendance.HolidaySet(ctx, tx, start, end)
	if err != nil {
		return nil, err
	}
	planner, err := attendance.LoadPlanner(ctx, tx, []uint64{actor.ID()}, start, end)
	if err != nil {
		return nil, err
	}
	emp := &actor.Employee
	v := &validated{lt: &lt, plan: dayPlan(planner, emp, start, end, isHol, st.Location)}
	if half && !end.Equal(start) {
		return v, apperr.BadRequest("HALF_DAY_ONE_DATE", "A half day covers a single date.")
	}

	today := timeutil.DateOf(s.Now(), st.Location)
	if start.Before(today.AddDate(0, 0, -maxBackdateDays)) {
		return v, apperr.BadRequest("TOO_FAR_BACK", fmt.Sprintf("Leave can be back-dated at most %d days.", maxBackdateDays))
	}
	reason := strings.TrimSpace(in.Reason)
	if reason == "" {
		return v, apperr.BadRequest("REASON_REQUIRED", "Please give a reason.")
	}
	lr := &models.LeaveRequest{EmployeeID: actor.ID(), LeaveTypeID: lt.ID, StartDate: start, EndDate: end, DayType: dayType,
		Reason: reason, Status: models.LeavePending, LeaveType: &lt}
	if half {
		if !v.plan.Work {
			if v.plan.Holiday {
				return v, apperr.BadRequest("NO_WORKING_DAYS", "That date is a holiday.")
			}
			return v, apperr.BadRequest("NO_WORKING_DAYS", "You do not work on that date.")
		}
		h := v.plan.FirstHalf
		if dayType == models.LeaveSecondHalf {
			h = v.plan.SecondHalf
		}
		lr.HalfFrom, lr.HalfTo, lr.Days = &h[0], &h[1], 0.5
	} else {
		lr.Days = float64(planner.WorkingDays(emp, start, end, isHol))
		if lr.Days == 0 {
			return v, apperr.BadRequest("NO_WORKING_DAYS", "The selected dates contain no working days.")
		}
	}
	v.req = lr
	if err := checkOverlap(ctx, tx, lr); err != nil {
		return v, err
	}
	bal, err := s.checkPolicy(ctx, tx, actor, &lt, start, today, lr.Days)
	v.bal = bal
	if err != nil {
		return v, err
	}
	return v, nil
}

// dayPlan describes the first requested date's shift and its halves.
func dayPlan(p *attendance.Planner, emp *models.Employee, start, end time.Time, isHol func(time.Time) bool, loc *time.Location) *DayPlan {
	d := p.Day(emp, start)
	plan := &DayPlan{Date: start, Work: d.Work && !isHol(start), Holiday: isHol(start), WorkType: models.WorkNormal}
	if d.Work {
		plan.ShiftName, plan.Start, plan.End = d.ShiftName, d.Start, d.End
		if d.CrossDay {
			plan.WorkType = models.WorkCrossDay
		}
		if first, second, next, err := d.Halves(start, loc); err == nil {
			plan.FirstHalf, plan.SecondHalf, plan.SecondNext = first, second, next
		}
	}
	for day := start; !day.After(end); day = day.AddDate(0, 0, 1) {
		if x := p.Day(emp, day); x.Work && x.CrossDay && !isHol(day) {
			plan.CrossDays = true
			break
		}
	}
	return plan
}

// checkOverlap refuses a request that overlaps a pending or approved one; only the
// two different halves of the same date may both be taken.
func checkOverlap(ctx context.Context, tx *gorm.DB, lr *models.LeaveRequest) error {
	var others []models.LeaveRequest
	if err := tx.WithContext(ctx).Where("employee_id = ? AND status IN ? AND start_date <= ? AND end_date >= ?",
		lr.EmployeeID, []string{models.LeavePending, models.LeaveApproved}, lr.EndDate, lr.StartDate).Find(&others).Error; err != nil {
		return err
	}
	for _, o := range others {
		if lr.IsHalfDay() && o.IsHalfDay() && o.DayType != lr.DayType {
			continue
		}
		if o.IsHalfDay() {
			return apperr.Conflict("OVERLAP", fmt.Sprintf("You already have a pending or approved half day on %s.", timeutil.FormatDate(o.StartDate)))
		}
		return apperr.Conflict("OVERLAP", "You already have a pending or approved leave covering these dates.")
	}
	return nil
}

func (s *Service) Submit(ctx context.Context, actor *auth.Actor, in SubmitInput) (*models.LeaveRequest, error) {
	var out *models.LeaveRequest
	var lt *models.LeaveType
	err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		// Serialize an employee's submissions so the overlap check is reliable.
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Select("id").First(&models.Employee{}, actor.ID()).Error; err != nil {
			return err
		}
		v, err := s.validate(ctx, tx, actor, in)
		if err != nil {
			return err
		}
		lr, t := v.req, v.lt
		lr.LeaveType = nil
		if t.Approval == models.LeaveApprovalAuto { // the type's policy: approved on submission
			now := s.Now()
			lr.Status, lr.ReviewedAt, lr.ReviewNote = models.LeaveApproved, &now, "Approved automatically (leave policy)"
		}
		if err := tx.Create(lr).Error; err != nil {
			return err
		}
		if err := audit.Write(ctx, tx, actor, audit.Entry{Action: audit.ActionLeaveSubmit, EntityType: "leave", EntityID: lr.ID, TargetEmployeeID: actor.ID(),
			Details: map[string]any{"type": t.Code, "start": lr.StartDate.Format("2006-01-02"), "end": lr.EndDate.Format("2006-01-02"),
				"day_type": lr.DayType, "days": lr.Days}}); err != nil {
			return err
		}
		if lr.Status == models.LeaveApproved {
			emp := actor.Employee
			if err := s.applyApproved(ctx, tx, &emp, lr, t.Name); err != nil {
				return err
			}
			if err := audit.Write(ctx, tx, nil, audit.Entry{Action: audit.ActionLeaveAutoApprove, EntityType: "leave", EntityID: lr.ID, TargetEmployeeID: actor.ID(),
				Details: map[string]any{"type": t.Code}}); err != nil {
				return err
			}
		}
		out, lt = lr, t
		return nil
	})
	if err != nil {
		return nil, err
	}
	out.LeaveType = lt
	if out.Status == models.LeaveApproved {
		s.notify.LeaveReviewed(*out)
	} else {
		s.notify.LeaveSubmitted(*out, actor.Employee, lt.Name)
	}
	return out, nil
}

// applyApproved records approved leave on attendance: full days become LEAVE; a
// half day re-measures late/early against the half still worked.
func (s *Service) applyApproved(ctx context.Context, tx *gorm.DB, emp *models.Employee, lr *models.LeaveRequest, typeName string) error {
	note := fmt.Sprintf("Leave #%d (%s)", lr.ID, typeName)
	if lr.IsHalfDay() {
		st, err := s.settings.Get(ctx)
		if err != nil {
			return err
		}
		return attendance.ApplyHalfDayLeave(ctx, tx, emp, lr.StartDate, lr.DayType, st.Location, note)
	}
	isHol, err := attendance.HolidaySet(ctx, tx, lr.StartDate, lr.EndDate)
	if err != nil {
		return err
	}
	return attendance.MarkLeaveDays(ctx, tx, emp, lr.StartDate, lr.EndDate, isHol, note)
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
			if err := s.applyApproved(ctx, tx, lr.Employee, &lr, lr.LeaveType.Name); err != nil {
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
	DayType    string `form:"day_type"` // FULL | HALF
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
	switch strings.ToUpper(f.DayType) {
	case "FULL":
		q = q.Where("day_type = ?", models.LeaveFullDay)
	case "HALF":
		q = q.Where("day_type <> ?", models.LeaveFullDay)
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
