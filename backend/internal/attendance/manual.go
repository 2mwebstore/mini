package attendance

import (
	"context"
	"errors"
	"strings"
	"time"

	"attendance/backend/internal/apperr"
	"attendance/backend/internal/audit"
	"attendance/backend/internal/auth"
	"attendance/backend/internal/models"
	"attendance/backend/internal/timeutil"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// ManualInput creates or corrects one employee-day. Every call is audited with
// before/after values and the mandatory reason.
type ManualInput struct {
	EmployeeID uint64 `json:"employee_id" binding:"required"`
	Date       string `json:"date" binding:"required"` // DD-MM-YYYY or YYYY-MM-DD
	CheckIn    string `json:"check_in"`                // HH:MM local, optional
	CheckOut   string `json:"check_out"`               // HH:MM local, optional
	Status     string `json:"status"`                  // optional override
	Reason     string `json:"reason" binding:"required,max=500"`
}

var nonWorkStatuses = map[string]bool{models.StatusAbsent: true, models.StatusLeave: true, models.StatusHoliday: true, models.StatusWeekend: true}
var workStatuses = map[string]bool{models.StatusPresent: true, models.StatusLate: true, models.StatusEarlyLeave: true}

func (s *Service) Manual(ctx context.Context, actor *auth.Actor, in ManualInput) (*models.Attendance, error) {
	if !actor.Has(auth.PermAttendanceEdit) {
		return nil, apperr.ErrForbidden
	}
	in.Reason = strings.TrimSpace(in.Reason)
	if in.Reason == "" {
		return nil, apperr.BadRequest("REASON_REQUIRED", "A reason is required for manual changes.")
	}
	in.Status = strings.ToUpper(strings.TrimSpace(in.Status))
	if in.Status != "" && !nonWorkStatuses[in.Status] && !workStatuses[in.Status] {
		return nil, apperr.BadRequest("INVALID_STATUS", "Unknown status.")
	}
	date, err := timeutil.ParseDate(in.Date)
	if err != nil {
		return nil, apperr.BadRequest("INVALID_DATE", err.Error())
	}
	st, err := s.settings.Get(ctx)
	if err != nil {
		return nil, err
	}
	loc := st.Location
	if date.After(timeutil.DateOf(s.Now(), loc)) {
		return nil, apperr.BadRequest("FUTURE_DATE", "Cannot record attendance for a future date.")
	}

	var emp models.Employee
	if err := s.db.WithContext(ctx).First(&emp, in.EmployeeID).Error; err != nil {
		return nil, apperr.NotFound("EMPLOYEE_NOT_FOUND", "Employee not found.")
	}
	if !actor.CanView(&emp) {
		return nil, apperr.ErrForbidden
	}
	plan, err := LoadPlanner(ctx, s.db, []uint64{emp.ID}, date, date)
	if err != nil {
		return nil, err
	}
	day := plan.Day(&emp, date)
	scheduled := day.Work
	if hol, _ := s.isHoliday(ctx, date); hol {
		scheduled = false
	}

	parse := func(hhmm string) (*time.Time, error) {
		if strings.TrimSpace(hhmm) == "" {
			return nil, nil
		}
		t, err := timeutil.At(date, hhmm, loc)
		if err != nil {
			return nil, apperr.BadRequest("INVALID_TIME", err.Error())
		}
		return &t, nil
	}
	inAt, err := parse(in.CheckIn)
	if err != nil {
		return nil, err
	}
	outAt, err := parse(in.CheckOut)
	if err != nil {
		return nil, err
	}
	if nonWorkStatuses[in.Status] && (inAt != nil || outAt != nil) {
		return nil, apperr.BadRequest("TIMES_NOT_ALLOWED", "Leave check-in/check-out empty for status "+in.Status+".")
	}

	now := s.Now()
	var result models.Attendance
	err = s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var att models.Attendance
		err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("employee_id = ? AND work_date = ?", emp.ID, date).First(&att).Error
		found := err == nil
		if err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
			return err
		}
		before := snapshot(&att, found, loc)

		if nonWorkStatuses[in.Status] {
			att.CheckInAt, att.CheckOutAt = nil, nil
			att.WorkingMinutes, att.LateMinutes, att.EarlyLeaveMinutes = 0, 0, 0
			att.Status = in.Status
		} else {
			if inAt != nil {
				att.CheckInAt = inAt
			}
			if outAt != nil {
				out := *outAt
				// A night shift's check-out time is on the next morning (22:00 -> 06:00).
				if day.CrossDay && att.CheckInAt != nil && !out.After(*att.CheckInAt) {
					out = out.AddDate(0, 0, 1)
				}
				att.CheckOutAt = &out
			}
			if att.CheckInAt == nil {
				return apperr.BadRequest("CHECK_IN_REQUIRED", "Check-in time is required (or set a status such as ABSENT).")
			}
			if att.CheckOutAt != nil && !att.CheckOutAt.After(*att.CheckInAt) {
				return apperr.BadRequest("INVALID_RANGE", "Check-out must be after check-in.")
			}
			att.LateMinutes, att.EarlyLeaveMinutes, att.WorkingMinutes = 0, 0, 0
			if scheduled {
				att.LateMinutes = day.LateMinutes(date, *att.CheckInAt, loc)
			}
			if att.CheckOutAt != nil {
				att.WorkingMinutes = WorkingMinutes(*att.CheckInAt, *att.CheckOutAt)
				if scheduled {
					att.EarlyLeaveMinutes = day.EarlyLeaveMinutes(date, *att.CheckOutAt, loc)
				}
			}
			att.Status = DeriveStatus(att.LateMinutes, att.EarlyLeaveMinutes)
			if in.Status != "" {
				att.Status = in.Status
			}
		}
		id := actor.ID()
		att.EmployeeID, att.WorkDate = emp.ID, date
		att.Source, att.Note = models.SourceManual, in.Reason
		att.ConfirmationStatus, att.ConfirmedBy, att.ConfirmedAt = models.ConfConfirmed, &id, &now
		if found {
			// An admin correction settles any open review for that day.
			if err := tx.Model(&models.AttendanceConfirmation{}).
				Where("attendance_id = ? AND status IN ?", att.ID, []string{models.ConfPending, models.ConfRejected}).
				Updates(map[string]any{"status": models.ConfSuperseded, "reviewed_by": id, "reviewed_at": now, "reason": "Manual correction: " + in.Reason}).Error; err != nil {
				return err
			}
			if err := tx.Omit(clause.Associations).Save(&att).Error; err != nil {
				return err
			}
		} else if err := tx.Omit(clause.Associations).Create(&att).Error; err != nil {
			return err
		}
		action := audit.ActionManualCreate
		if found {
			action = audit.ActionManualEdit
		}
		if err := audit.Write(ctx, tx, actor, audit.Entry{Action: action, EntityType: "attendance", EntityID: att.ID, TargetEmployeeID: emp.ID,
			Details: map[string]any{"reason": in.Reason, "before": before, "after": snapshot(&att, true, loc)}}); err != nil {
			return err
		}
		result = att
		return nil
	})
	if err != nil {
		return nil, err
	}
	return &result, nil
}

func snapshot(a *models.Attendance, found bool, loc *time.Location) map[string]any {
	if !found {
		return nil
	}
	return map[string]any{
		"date": a.WorkDate.Format("2006-01-02"), "check_in": timeutil.FormatClock24(a.CheckInAt, loc),
		"check_out": timeutil.FormatClock24(a.CheckOutAt, loc), "status": a.Status,
		"confirmation": a.ConfirmationStatus, "working_minutes": a.WorkingMinutes, "late_minutes": a.LateMinutes,
	}
}

// MarkLeaveDays sets status LEAVE on working days of an approved leave that have
// no check-in. Called inside the leave approval transaction.
func MarkLeaveDays(ctx context.Context, tx *gorm.DB, emp *models.Employee, from, to time.Time, isHoliday func(time.Time) bool, note string) error {
	plan, err := LoadPlanner(ctx, tx, []uint64{emp.ID}, from, to)
	if err != nil {
		return err
	}
	for d := from; !d.After(to); d = d.AddDate(0, 0, 1) {
		if !plan.Day(emp, d).Work || isHoliday(d) {
			continue
		}
		var att models.Attendance
		err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("employee_id = ? AND work_date = ?", emp.ID, d).First(&att).Error
		switch {
		case errors.Is(err, gorm.ErrRecordNotFound):
			att = models.Attendance{EmployeeID: emp.ID, WorkDate: d, Status: models.StatusLeave,
				ConfirmationStatus: models.ConfConfirmed, Source: models.SourceSystem, Note: note}
			if err := tx.Omit(clause.Associations).Create(&att).Error; err != nil {
				return err
			}
		case err != nil:
			return err
		case att.CheckInAt == nil:
			if err := tx.Model(&att).Updates(map[string]any{"status": models.StatusLeave, "note": note}).Error; err != nil {
				return err
			}
		}
	}
	return nil
}

// WorkingDays counts emp's scheduled working days in [from, to], excluding holidays.
func WorkingDays(ctx context.Context, db *gorm.DB, emp *models.Employee, from, to time.Time, isHoliday func(time.Time) bool) (int, error) {
	plan, err := LoadPlanner(ctx, db, []uint64{emp.ID}, from, to)
	if err != nil {
		return 0, err
	}
	return plan.WorkingDays(emp, from, to, isHoliday), nil
}

// HolidaySet loads holidays in a range into a lookup function.
func HolidaySet(ctx context.Context, db *gorm.DB, from, to time.Time) (func(time.Time) bool, error) {
	var hs []models.Holiday
	if err := db.WithContext(ctx).Where("date BETWEEN ? AND ?", from, to).Find(&hs).Error; err != nil {
		return nil, err
	}
	set := map[string]bool{}
	for _, h := range hs {
		set[h.Date.Format("2006-01-02")] = true
	}
	return func(d time.Time) bool { return set[d.Format("2006-01-02")] }, nil
}
