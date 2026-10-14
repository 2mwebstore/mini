// Package attendance implements check-in/out, admin review and manual corrections.
package attendance

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"attendance/backend/internal/apperr"
	"attendance/backend/internal/audit"
	"attendance/backend/internal/auth"
	"attendance/backend/internal/models"
	"attendance/backend/internal/notification"
	"attendance/backend/internal/settings"
	"attendance/backend/internal/storage"
	"attendance/backend/internal/timeutil"

	mysqldrv "github.com/go-sql-driver/mysql"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

type Service struct {
	db       *gorm.DB
	settings *settings.Service
	store    storage.Store
	notify   *notification.Notifier
	Now      func() time.Time
}

func NewService(db *gorm.DB, st *settings.Service, store storage.Store, n *notification.Notifier) *Service {
	return &Service{db: db, settings: st, store: store, notify: n, Now: func() time.Time { return time.Now().UTC() }}
}

// ---------- inputs / outputs ----------

type LocationInput struct {
	Latitude  float64  `json:"latitude" binding:"gte=-90,lte=90"`
	Longitude float64  `json:"longitude" binding:"gte=-180,lte=180"`
	Accuracy  *float64 `json:"accuracy"`
}

// SubmitInput is a check-in/out from the Mini App: the device location (when
// shared) and, optionally, a photo — the Mini App no longer takes one.
type SubmitInput struct {
	PhotoData []byte
	Location  *LocationInput
	Reason    string // the employee's reason; required when late / leaving early
}

// maxReason caps the employee's reason for a check-in / check-out.
const maxReason = 255

// MaxUploadBytes caps uploaded photos.
const MaxUploadBytes = 8 << 20

type SubmitResult struct {
	Attendance    models.Attendance `json:"attendance"`
	Type          string            `json:"type"`
	Pending       bool              `json:"pending"`
	Resubmission  bool              `json:"resubmission"`
	LocationLabel string            `json:"location_label"`
	Employee      models.Employee   `json:"employee"`
	Timezone      string            `json:"timezone"`
}

// PrecheckResult tells the Mini App whether check-in/out is allowed now and whether to ask for location.
type PrecheckResult struct {
	Allowed     bool   `json:"allowed"`
	NeedGPS     bool   `json:"need_gps"`
	GPSRequired bool   `json:"gps_required"`
	Message     string `json:"message,omitempty"`
	// Right now: how late a check-in / how early a check-out would be. Either needs a reason.
	LateMinutes    int  `json:"late_minutes,omitempty"`
	EarlyMinutes   int  `json:"early_minutes,omitempty"`
	ReasonRequired bool `json:"reason_required"`
}

// ---------- calendar helpers ----------

// isHoliday: date is a holiday of the branch (each branch keeps its own holidays).
func (s *Service) isHoliday(ctx context.Context, branchID *uint64, date time.Time) (bool, string) {
	if branchID == nil {
		return false, ""
	}
	var h models.Holiday
	if err := s.db.WithContext(ctx).Where("branch_id = ? AND date = ?", *branchID, date).First(&h).Error; err == nil {
		return true, h.Name
	}
	return false, ""
}

// onApprovedLeave: approved full-day leave covers date (a half day still works the other half).
func (s *Service) onApprovedLeave(ctx context.Context, tx *gorm.DB, employeeID uint64, date time.Time) bool {
	var n int64
	tx.WithContext(ctx).Model(&models.LeaveRequest{}).
		Where("employee_id = ? AND status = ? AND day_type = ? AND start_date <= ? AND end_date >= ?",
			employeeID, models.LeaveApproved, models.LeaveFullDay, date, date).
		Count(&n)
	return n > 0
}

// HalfDayLeave is the employee's approved half-day leave on date, if any.
func HalfDayLeave(ctx context.Context, db *gorm.DB, employeeID uint64, date time.Time) *models.LeaveRequest {
	var lr models.LeaveRequest
	err := db.WithContext(ctx).Preload("LeaveType").
		Where("employee_id = ? AND status = ? AND day_type IN ? AND start_date = ?",
			employeeID, models.LeaveApproved, []string{models.LeaveFirstHalf, models.LeaveSecondHalf}, date).
		First(&lr).Error
	if err != nil {
		return nil
	}
	return &lr
}

func latestConfirmation(confs []models.AttendanceConfirmation, typ string) *models.AttendanceConfirmation {
	var latest *models.AttendanceConfirmation
	for i := range confs {
		c := &confs[i]
		if c.Type == typ && c.Status != models.ConfSuperseded && (latest == nil || c.ID > latest.ID) {
			latest = c
		}
	}
	return latest
}

// ---------- GPS rules ----------

// GPSRules is the location policy for one person's check-ins: their branch's.
type GPSRules struct {
	Enabled, Required, RejectOutside bool
}

// GPSRulesFor: the branch's GPS rules (set on the branch page). No branch, or none
// set: location is not asked for. Rejecting outside needs the branch's office.
func GPSRulesFor(b *models.Branch) GPSRules {
	if b == nil || b.GPSEnabled == nil || !*b.GPSEnabled {
		return GPSRules{}
	}
	r := GPSRules{Enabled: true}
	if b.GPSRequired != nil {
		r.Required = *b.GPSRequired
	}
	if b.GPSRejectOutside != nil {
		r.RejectOutside = *b.GPSRejectOutside && b.HasOffice()
	}
	return r
}

// ---------- which work date an event belongs to ----------

const (
	// A night shift's check-out still counts for it until this long after it ends.
	nightCheckoutWindow = 8 * time.Hour
	// A check-in this close to today's own shift is for today, even while
	// yesterday's night shift has not ended yet.
	earlyCheckinWindow = 2 * time.Hour
)

// shiftFor decides the work date of a check-in/out made at now, and the hours to
// work that date: the shift, less the half on leave for an approved half day.
func (s *Service) shiftFor(ctx context.Context, db *gorm.DB, emp *models.Employee, now time.Time, loc *time.Location, typ string) (time.Time, Day, error) {
	date, day, err := s.workDateFor(ctx, db, emp, now, loc, typ)
	if err != nil || !day.Work {
		return date, day, err
	}
	if lr := HalfDayLeave(ctx, db, emp.ID, date); lr != nil {
		day = day.WithHalfOff(lr.DayType, date, loc)
	}
	return date, day, nil
}

// workDateFor: normally today; but a night shift (cross-day) that started yesterday
// still owns a late check-in made before it ends, and its check-out until a few
// hours after it ends.
func (s *Service) workDateFor(ctx context.Context, db *gorm.DB, emp *models.Employee, now time.Time, loc *time.Location, typ string) (time.Time, Day, error) {
	today := timeutil.DateOf(now, loc)
	yday := today.AddDate(0, 0, -1)
	plan, err := LoadPlanner(ctx, db, []uint64{emp.ID}, yday, today)
	if err != nil {
		return today, Day{}, err
	}
	td := plan.Day(emp, today)
	y := plan.Day(emp, yday)
	if !y.Work || !y.CrossDay || (typ != models.TypeCheckIn && typ != models.TypeCheckOut) {
		return today, td, nil
	}
	end, err := y.EndAt(yday, loc)
	if err != nil {
		return today, td, nil
	}
	var att models.Attendance
	err = db.WithContext(ctx).Preload("Confirmations").Where("employee_id = ? AND work_date = ?", emp.ID, yday).First(&att).Error
	found := err == nil
	if err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
		return today, td, err
	}
	switch typ {
	case models.TypeCheckOut:
		if found && checkOutBlocker(true, &att) == nil && now.Before(end.Add(nightCheckoutWindow)) {
			return yday, y, nil
		}
	case models.TypeCheckIn:
		if !now.Before(end) || checkInBlocker(found, &att) != nil || s.onApprovedLeave(ctx, db, emp.ID, yday) {
			break
		}
		if td.Work {
			if start, err := td.StartAt(today, loc); err == nil && !now.Before(start.Add(-earlyCheckinWindow)) {
				break // early for today's shift rather than very late for last night's
			}
		}
		return yday, y, nil
	}
	return today, td, nil
}

// ---------- precheck ----------

func (s *Service) Precheck(ctx context.Context, actor *auth.Actor, typ string) (*PrecheckResult, error) {
	if !actor.Has(auth.PermAttendanceSelf) {
		return nil, apperr.ErrForbidden
	}
	st, err := s.settings.Get(ctx)
	if err != nil {
		return nil, err
	}
	gps := GPSRulesFor(actor.Employee.Branch)
	res := &PrecheckResult{Allowed: true, NeedGPS: gps.Enabled, GPSRequired: gps.Required}
	now := s.Now()
	date, day, err := s.shiftFor(ctx, s.db, &actor.Employee, now, st.Location, typ)
	if err != nil {
		return nil, err
	}
	res.LateMinutes, res.EarlyMinutes = s.offSchedule(ctx, &actor.Employee, date, day, now, st.Location, typ)
	res.ReasonRequired = res.LateMinutes > 0 || res.EarlyMinutes > 0
	var att models.Attendance
	err = s.db.WithContext(ctx).Preload("Confirmations").Where("employee_id = ? AND work_date = ?", actor.ID(), date).First(&att).Error
	found := err == nil
	if err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, err
	}
	block := func(e *apperr.Error) (*PrecheckResult, error) {
		res.Allowed, res.Message = false, e.Message
		return res, nil
	}
	switch typ {
	case models.TypeCheckIn:
		if e := checkInBlocker(found, &att); e != nil {
			return block(e)
		}
		if s.onApprovedLeave(ctx, s.db, actor.ID(), date) {
			return block(errOnLeave)
		}
	case models.TypeCheckOut:
		if e := checkOutBlocker(found, &att); e != nil {
			return block(e)
		}
	default:
		return nil, apperr.BadRequest("INVALID_TYPE", "Type must be CHECK_IN or CHECK_OUT.")
	}
	return res, nil
}

// offSchedule: how late a check-in, or how early a check-out, made at now would be
// (0 on days off and holidays).
func (s *Service) offSchedule(ctx context.Context, emp *models.Employee, date time.Time, day Day, now time.Time, loc *time.Location, typ string) (late, early int) {
	if !day.Work {
		return 0, 0
	}
	if hol, _ := s.isHoliday(ctx, emp.BranchID, date); hol {
		return 0, 0
	}
	if typ == models.TypeCheckIn {
		return day.LateMinutes(date, now, loc), 0
	}
	return 0, day.EarlyLeaveMinutes(date, now, loc)
}

// minutesText: "15 min", "1 h", "2 h 5 min".
func minutesText(m int) string {
	h, r := m/60, m%60
	switch {
	case h == 0:
		return fmt.Sprintf("%d min", r)
	case r == 0:
		return fmt.Sprintf("%d h", h)
	}
	return fmt.Sprintf("%d h %d min", h, r)
}

// TodayStatus is the employee's own day at a glance (Mini App home screen). Date
// is the work date of the shift in progress: yesterday's while a night shift that
// started yesterday is still running or waiting for its check-out.
type TodayStatus struct {
	Date         time.Time          `json:"date"`
	WorkDay      bool               `json:"work_day"`
	Holiday      string             `json:"holiday,omitempty"`
	ScheduleFrom string             `json:"schedule_start"`
	ScheduleTo   string             `json:"schedule_end"`
	CrossDay     bool               `json:"cross_day"` // the shift ends the next day
	ShiftName    string             `json:"shift_name"`
	Special      string             `json:"special,omitempty"` // reason of a special schedule in force
	GraceMinutes int                `json:"grace_minutes"`
	Attendance   *models.Attendance `json:"attendance"` // nil until something is recorded
	CheckIn      *PrecheckResult    `json:"check_in"`
	CheckOut     *PrecheckResult    `json:"check_out"`
	HalfLeave    *HalfLeave         `json:"half_leave,omitempty"` // approved half-day leave on Date
}

// HalfLeave is the half of the day an employee has off; the schedule shown is the other half.
type HalfLeave struct {
	DayType    string `json:"day_type"` // FIRST_HALF | SECOND_HALF
	From       string `json:"from"`
	To         string `json:"to"`
	LeaveType  string `json:"leave_type"`
	ShiftStart string `json:"shift_start"` // the whole shift, for naming the half (morning / first half)
	WorkType   string `json:"work_type"`
}

func (s *Service) Today(ctx context.Context, actor *auth.Actor) (*TodayStatus, error) {
	if !actor.Has(auth.PermAttendanceSelf) {
		return nil, apperr.ErrForbidden
	}
	st, err := s.settings.Get(ctx)
	if err != nil {
		return nil, err
	}
	now := s.Now()
	date, day, err := s.shiftFor(ctx, s.db, &actor.Employee, now, st.Location, models.TypeCheckOut)
	if err != nil {
		return nil, err
	}
	if today := timeutil.DateOf(now, st.Location); date.Equal(today) {
		if date, day, err = s.shiftFor(ctx, s.db, &actor.Employee, now, st.Location, models.TypeCheckIn); err != nil {
			return nil, err
		}
	}
	out := &TodayStatus{Date: date, WorkDay: day.Work, ScheduleFrom: day.Start, ScheduleTo: day.End, CrossDay: day.CrossDay,
		ShiftName: day.ShiftName, GraceMinutes: day.Grace}
	if day.Source == PlanSpecial {
		out.Special = day.Note
		if out.Special == "" {
			out.Special = "Special schedule"
		}
	}
	if hol, name := s.isHoliday(ctx, actor.Employee.BranchID, date); hol {
		out.WorkDay, out.Holiday = false, name
	}
	if lr := HalfDayLeave(ctx, s.db, actor.ID(), date); lr != nil && out.WorkDay {
		h := &HalfLeave{DayType: lr.DayType, WorkType: models.WorkNormal}
		if plan, err := LoadPlanner(ctx, s.db, []uint64{actor.ID()}, date, date); err == nil {
			whole := plan.Day(&actor.Employee, date)
			h.ShiftStart = whole.Start
			if whole.CrossDay {
				h.WorkType = models.WorkCrossDay
			}
		}
		if lr.HalfFrom != nil && lr.HalfTo != nil {
			h.From, h.To = *lr.HalfFrom, *lr.HalfTo
		}
		if lr.LeaveType != nil {
			h.LeaveType = lr.LeaveType.Name
		}
		out.HalfLeave = h
	}
	var att models.Attendance
	err = s.db.WithContext(ctx).Preload("Confirmations").Where("employee_id = ? AND work_date = ?", actor.ID(), date).First(&att).Error
	if err == nil {
		out.Attendance = &att
	} else if !errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, err
	}
	if out.CheckIn, err = s.Precheck(ctx, actor, models.TypeCheckIn); err != nil {
		return nil, err
	}
	if out.CheckOut, err = s.Precheck(ctx, actor, models.TypeCheckOut); err != nil {
		return nil, err
	}
	return out, nil
}

var (
	errAlreadyIn   = apperr.Conflict("ALREADY_CHECKED_IN", "You have already checked in today.")
	errAlreadyOut  = apperr.Conflict("ALREADY_CHECKED_OUT", "You have already checked out today.")
	errNoCheckIn   = apperr.Conflict("NO_CHECK_IN", "You have not checked in today. Please check in first, or ask an administrator to correct your attendance.")
	errInRejected  = apperr.Conflict("CHECK_IN_REJECTED", "Your check-in was rejected. Please submit a new check-in first.")
	errOnLeave     = apperr.Conflict("ON_LEAVE", "You are on approved leave today. Ask an administrator if you need to work today.")
	errDayRecorded = apperr.Conflict("DAY_RECORDED", "Today's attendance was already recorded by an administrator.")
)

// checkInBlocker returns nil if a check-in (or re-submission of a rejected one) is allowed.
func checkInBlocker(found bool, att *models.Attendance) *apperr.Error {
	if !found {
		return nil
	}
	if att.Status == models.StatusLeave {
		return errOnLeave
	}
	if att.CheckInAt == nil {
		if att.Source == models.SourceManual {
			return errDayRecorded
		}
		return nil // e.g. a system ABSENT placeholder
	}
	if att.CheckOutAt == nil {
		if c := latestConfirmation(att.Confirmations, models.TypeCheckIn); c != nil && c.Status == models.ConfRejected {
			return nil
		}
	}
	return errAlreadyIn
}

func checkOutBlocker(found bool, att *models.Attendance) *apperr.Error {
	if !found || att.CheckInAt == nil {
		return errNoCheckIn
	}
	if c := latestConfirmation(att.Confirmations, models.TypeCheckIn); c != nil && c.Status == models.ConfRejected {
		return errInRejected
	}
	if att.CheckOutAt != nil {
		if c := latestConfirmation(att.Confirmations, models.TypeCheckOut); c != nil && c.Status == models.ConfRejected {
			return nil
		}
		return errAlreadyOut
	}
	return nil
}

// ---------- submit (check-in / check-out) ----------

func (s *Service) CheckIn(ctx context.Context, actor *auth.Actor, in SubmitInput) (*SubmitResult, error) {
	return s.submit(ctx, actor, models.TypeCheckIn, in)
}

func (s *Service) CheckOut(ctx context.Context, actor *auth.Actor, in SubmitInput) (*SubmitResult, error) {
	return s.submit(ctx, actor, models.TypeCheckOut, in)
}

func (s *Service) submit(ctx context.Context, actor *auth.Actor, typ string, in SubmitInput) (*SubmitResult, error) {
	if !actor.Has(auth.PermAttendanceSelf) {
		return nil, apperr.ErrForbidden
	}
	st, err := s.settings.Get(ctx)
	if err != nil {
		return nil, err
	}
	loc := st.Location
	now := s.Now()
	emp := actor.Employee
	date, day, err := s.shiftFor(ctx, s.db, &emp, now, loc, typ)
	if err != nil {
		return nil, err
	}

	// --- location policy: the branch's rules and office ---
	var locRow *models.AttendanceLocation
	label := "Not provided"
	gps := GPSRulesFor(emp.Branch)
	if gps.Enabled {
		if in.Location == nil {
			if gps.Required {
				return nil, apperr.BadRequest("LOCATION_REQUIRED", "Please share your current location.")
			}
		} else if !emp.Branch.HasOffice() { // no office set: nothing to measure against
			locRow = &models.AttendanceLocation{Type: typ, Latitude: in.Location.Latitude, Longitude: in.Location.Longitude, Accuracy: in.Location.Accuracy, CreatedAt: now}
			label = "Received"
		} else {
			officeLat, officeLng, radius := *emp.Branch.OfficeLatitude, *emp.Branch.OfficeLongitude, *emp.Branch.AllowedRadiusMeters
			dist := DistanceMeters(in.Location.Latitude, in.Location.Longitude, officeLat, officeLng)
			inside := dist <= radius
			if !inside && gps.RejectOutside {
				return nil, apperr.BadRequest("OUTSIDE_OFFICE", fmt.Sprintf("You are %.0f m from the office (allowed: %.0f m).", dist, radius))
			}
			locRow = &models.AttendanceLocation{Type: typ, Latitude: in.Location.Latitude, Longitude: in.Location.Longitude,
				Accuracy: in.Location.Accuracy, DistanceMeters: &dist, InsideOffice: &inside, CreatedAt: now}
			if inside {
				label = fmt.Sprintf("Inside office radius (%.0f m)", dist)
			} else {
				label = fmt.Sprintf("Outside office radius (%.0f m)", dist)
			}
		}
	} else if in.Location != nil {
		locRow = &models.AttendanceLocation{Type: typ, Latitude: in.Location.Latitude, Longitude: in.Location.Longitude, Accuracy: in.Location.Accuracy, CreatedAt: now}
		label = "Received"
	}

	// --- cheap state check before storing anything ---
	if pre, err := s.Precheck(ctx, actor, typ); err != nil {
		return nil, err
	} else if !pre.Allowed {
		return nil, apperr.Conflict("NOT_ALLOWED", pre.Message)
	}

	// --- the employee's reason: required when late / leaving early ---
	reason := strings.TrimSpace(in.Reason)
	if len([]rune(reason)) > maxReason {
		return nil, apperr.BadRequest("REASON_TOO_LONG", fmt.Sprintf("The reason can be at most %d characters.", maxReason))
	}
	if late, early := s.offSchedule(ctx, &emp, date, day, now, loc, typ); reason == "" && (late > 0 || early > 0) {
		if late > 0 {
			return nil, apperr.BadRequest("REASON_REQUIRED", fmt.Sprintf("You are %s late. Please say why.", minutesText(late)))
		}
		return nil, apperr.BadRequest("REASON_REQUIRED", fmt.Sprintf("You are leaving %s early. Please say why.", minutesText(early)))
	}

	// --- optional photo: validate, reject re-used images, store ---
	var photo *models.AttendancePhoto
	if len(in.PhotoData) > 0 {
		if photo, err = s.storePhoto(ctx, emp, typ, date, now, in.PhotoData); err != nil {
			return nil, err
		}
	}

	// --- transactional state change ---
	confStatus := models.ConfConfirmed
	if st.ConfirmationRequired {
		confStatus = models.ConfPending
	}
	res := &SubmitResult{Type: typ, Pending: confStatus == models.ConfPending, LocationLabel: label, Employee: emp, Timezone: st.Timezone}
	var confID uint64

	err = s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var att models.Attendance
		err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Preload("Confirmations").
			Where("employee_id = ? AND work_date = ?", emp.ID, date).First(&att).Error
		found := err == nil
		if err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
			return err
		}
		scheduled := day.Work
		if hol, _ := s.isHoliday(ctx, emp.BranchID, date); hol {
			scheduled = false
		}

		switch typ {
		case models.TypeCheckIn:
			if e := checkInBlocker(found, &att); e != nil {
				return e
			}
			if s.onApprovedLeave(ctx, tx, emp.ID, date) {
				return errOnLeave
			}
			res.Resubmission = found && att.CheckInAt != nil
			late := 0
			if scheduled {
				late = day.LateMinutes(date, now, loc)
			}
			t := now
			att.EmployeeID, att.WorkDate, att.CheckInAt, att.LateMinutes, att.CheckInReason = emp.ID, date, &t, late, reason
			att.Status = DeriveStatus(late, 0)
			att.Source, att.ConfirmedBy, att.ConfirmedAt = models.SourceTelegram, nil, nil
			if found {
				if err := tx.Model(&models.AttendanceConfirmation{}).
					Where("attendance_id = ? AND type = ? AND status = ?", att.ID, typ, models.ConfRejected).
					Update("status", models.ConfSuperseded).Error; err != nil {
					return err
				}
				if err := tx.Omit(clause.Associations).Save(&att).Error; err != nil {
					return err
				}
			} else {
				att.ConfirmationStatus = confStatus
				if err := tx.Omit(clause.Associations).Create(&att).Error; err != nil {
					if isDuplicate(err) {
						return errAlreadyIn
					}
					return err
				}
			}
		case models.TypeCheckOut:
			if e := checkOutBlocker(found, &att); e != nil {
				return e
			}
			res.Resubmission = att.CheckOutAt != nil
			t := now
			att.CheckOutAt, att.CheckOutReason = &t, reason
			att.WorkingMinutes = WorkingMinutes(*att.CheckInAt, now)
			att.EarlyLeaveMinutes = 0
			if scheduled {
				att.EarlyLeaveMinutes = day.EarlyLeaveMinutes(date, now, loc)
			}
			att.Status = DeriveStatus(att.LateMinutes, att.EarlyLeaveMinutes)
			if err := tx.Model(&models.AttendanceConfirmation{}).
				Where("attendance_id = ? AND type = ? AND status = ?", att.ID, typ, models.ConfRejected).
				Update("status", models.ConfSuperseded).Error; err != nil {
				return err
			}
			if err := tx.Omit(clause.Associations).Save(&att).Error; err != nil {
				return err
			}
		}

		conf := models.AttendanceConfirmation{AttendanceID: att.ID, Type: typ, Status: confStatus}
		if photo != nil {
			photo.AttendanceID = att.ID
			if err := tx.Create(photo).Error; err != nil {
				return err
			}
			conf.PhotoID = &photo.ID
		}
		if locRow != nil {
			locRow.AttendanceID = att.ID
			if err := tx.Create(locRow).Error; err != nil {
				return err
			}
			conf.LocationID = &locRow.ID
		}
		if confStatus == models.ConfConfirmed {
			conf.ReviewedAt = &now
		}
		if err := tx.Create(&conf).Error; err != nil {
			return err
		}
		confID = conf.ID
		if err := recomputeConfirmation(tx, &att, nil, now); err != nil {
			return err
		}
		action := audit.ActionCheckIn
		if typ == models.TypeCheckOut {
			action = audit.ActionCheckOut
		}
		details := map[string]any{"at": now, "late_minutes": att.LateMinutes, "working_minutes": att.WorkingMinutes,
			"location": label, "resubmission": res.Resubmission}
		if photo != nil {
			details["photo_key"] = photo.StorageKey
		}
		if err := audit.Write(ctx, tx, actor, audit.Entry{Action: action, EntityType: "attendance", EntityID: att.ID, TargetEmployeeID: emp.ID,
			Details: details}); err != nil {
			return err
		}
		res.Attendance = att
		return nil
	})
	if err != nil {
		return nil, err
	}

	ev := notification.AttendanceEvent{Attendance: res.Attendance, Employee: emp, Type: typ, ConfirmationID: confID, Pending: res.Pending,
		LocationLabel: label, Loc: loc}
	if photo != nil {
		ev.PhotoID, ev.PhotoData = photo.ID, in.PhotoData
	}
	s.notify.AttendanceSubmitted(ev)
	return res, nil
}

var allowedImageTypes = map[string]string{"image/jpeg": "jpg", "image/png": "png", "image/webp": "webp"}

func (s *Service) storePhoto(ctx context.Context, emp models.Employee, typ string, date, now time.Time, data []byte) (*models.AttendancePhoto, error) {
	if len(data) > MaxUploadBytes {
		return nil, apperr.BadRequest("PHOTO_TOO_LARGE", "The photo is too large. Please take it again.")
	}
	ct := http.DetectContentType(data)
	ext, ok := allowedImageTypes[ct]
	if !ok {
		return nil, apperr.BadRequest("INVALID_PHOTO", "Please send a photo (JPEG/PNG).")
	}
	sum := sha256.Sum256(data)
	digest := hex.EncodeToString(sum[:])
	var reused int64
	if err := s.db.WithContext(ctx).Model(&models.AttendancePhoto{}).Where("content_sha256 = ?", digest).Count(&reused).Error; err != nil {
		return nil, err
	}
	if reused > 0 {
		return nil, apperr.BadRequest("PHOTO_REUSED", "This photo was already used. Please take a new photo.")
	}
	rnd := make([]byte, 6)
	_, _ = rand.Read(rnd)
	kind := "in"
	if typ == models.TypeCheckOut {
		kind = "out"
	}
	key := fmt.Sprintf("attendance/%s/%s/%s-%s-%s.%s", date.Format("2006/01/02"), sanitizeKey(emp.EmployeeCode), kind,
		now.Format("150405"), hex.EncodeToString(rnd), ext)
	if err := s.store.Put(ctx, key, data, ct); err != nil {
		slog.Error("photo upload failed", "key", key, "err", err)
		return nil, apperr.New(http.StatusServiceUnavailable, "STORAGE_UNAVAILABLE", "Photo storage is temporarily unavailable. Please try again.")
	}
	var tgID int64
	if emp.TelegramAccount != nil {
		tgID = emp.TelegramAccount.TelegramUserID
	}
	// telegram_file_id is filled in once the reviewer notification has sent the photo.
	return &models.AttendancePhoto{Type: typ, ContentSHA256: digest, TelegramUserID: tgID,
		StorageKey: key, StorageURL: s.store.URI(key), ContentType: ct, SizeBytes: int64(len(data)), CreatedAt: now}, nil
}

func sanitizeKey(s string) string {
	var b strings.Builder
	for _, r := range s {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || r == '-' || r == '_' {
			b.WriteRune(r)
		}
	}
	if b.Len() == 0 {
		return "emp"
	}
	return b.String()
}

// recomputeConfirmation derives attendance.confirmation_status from its reviews.
func recomputeConfirmation(tx *gorm.DB, att *models.Attendance, reviewer *uint64, now time.Time) error {
	var confs []models.AttendanceConfirmation
	if err := tx.Where("attendance_id = ? AND status <> ?", att.ID, models.ConfSuperseded).Find(&confs).Error; err != nil {
		return err
	}
	att.ConfirmationStatus = DeriveConfirmation(confs)
	updates := map[string]any{"confirmation_status": att.ConfirmationStatus}
	if att.ConfirmationStatus == models.ConfConfirmed {
		if reviewer != nil {
			att.ConfirmedBy, att.ConfirmedAt = reviewer, &now
			updates["confirmed_by"], updates["confirmed_at"] = *reviewer, now
		}
	} else {
		att.ConfirmedBy, att.ConfirmedAt = nil, nil
		updates["confirmed_by"], updates["confirmed_at"] = nil, nil
	}
	return tx.Model(&models.Attendance{}).Where("id = ?", att.ID).Updates(updates).Error
}

func isDuplicate(err error) bool {
	var me *mysqldrv.MySQLError
	return errors.As(err, &me) && me.Number == 1062
}

// ---------- review ----------

type ReviewResult struct {
	Attendance   models.Attendance             `json:"attendance"`
	Confirmation models.AttendanceConfirmation `json:"confirmation"`
	Employee     models.Employee               `json:"employee"`
	Reviewer     models.Employee               `json:"reviewer"`
	Timezone     string                        `json:"timezone"`
}

func (s *Service) Review(ctx context.Context, actor *auth.Actor, confID uint64, approve bool, reason string) (*ReviewResult, error) {
	if !actor.IsReviewer() {
		return nil, apperr.ErrForbidden
	}
	reason = strings.TrimSpace(reason)
	if !approve && reason == "" {
		return nil, apperr.BadRequest("REASON_REQUIRED", "Please give a reason for rejecting.")
	}
	if len(reason) > 500 {
		return nil, apperr.BadRequest("REASON_TOO_LONG", "Reason must be 500 characters or fewer.")
	}
	st, err := s.settings.Get(ctx)
	if err != nil {
		return nil, err
	}
	now := s.Now()
	res := &ReviewResult{Reviewer: actor.Employee, Timezone: st.Timezone}
	err = s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var conf models.AttendanceConfirmation
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).First(&conf, confID).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return apperr.NotFound("NOT_FOUND", "Confirmation not found.")
			}
			return err
		}
		var att models.Attendance
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Preload("Employee").First(&att, conf.AttendanceID).Error; err != nil {
			return err
		}
		if !actor.CanReview(att.Employee) {
			return apperr.ErrForbidden
		}
		if conf.Status != models.ConfPending {
			return alreadyReviewed(tx, &conf)
		}
		conf.Status = models.ConfRejected
		if approve {
			conf.Status = models.ConfConfirmed
		}
		id := actor.ID()
		conf.ReviewedBy, conf.ReviewedAt, conf.Reason = &id, &now, reason
		if err := tx.Save(&conf).Error; err != nil {
			return err
		}
		if err := recomputeConfirmation(tx, &att, &id, now); err != nil {
			return err
		}
		action := audit.ActionConfirm
		if !approve {
			action = audit.ActionReject
		}
		if err := audit.Write(ctx, tx, actor, audit.Entry{Action: action, EntityType: "attendance", EntityID: att.ID, TargetEmployeeID: att.EmployeeID,
			Details: map[string]any{"confirmation_id": conf.ID, "type": conf.Type, "reason": reason, "work_date": att.WorkDate.Format("2006-01-02")}}); err != nil {
			return err
		}
		res.Attendance, res.Confirmation, res.Employee = att, conf, *att.Employee
		return nil
	})
	if err != nil {
		return nil, err
	}
	s.notify.AttendanceReviewed(res.Attendance, res.Confirmation, actor.Employee, st.Location)
	return res, nil
}

func alreadyReviewed(tx *gorm.DB, conf *models.AttendanceConfirmation) error {
	if conf.Status == models.ConfSuperseded {
		return apperr.Conflict("SUPERSEDED", "The employee already re-submitted this record.")
	}
	who := "another reviewer"
	if conf.ReviewedBy != nil {
		var e models.Employee
		if tx.Select("full_name").First(&e, *conf.ReviewedBy).Error == nil {
			who = e.FullName
		}
	} else {
		who = "the system"
	}
	return apperr.Conflict("ALREADY_REVIEWED", fmt.Sprintf("Already %s by %s.", strings.ToLower(conf.Status), who))
}

// ---------- queries ----------

type PendingItem struct {
	ConfirmationID uint64     `json:"confirmation_id"`
	AttendanceID   uint64     `json:"attendance_id"`
	Type           string     `json:"type"`
	EmployeeID     uint64     `json:"employee_id"`
	EmployeeCode   string     `json:"employee_code"`
	FullName       string     `json:"full_name"`
	WorkDate       time.Time  `json:"work_date"`
	CheckInAt      *time.Time `json:"check_in_at"`
	CheckOutAt     *time.Time `json:"check_out_at"`
	LateMinutes    int        `json:"late_minutes"`
	EarlyMinutes   int        `json:"early_leave_minutes"`
	Reason         string     `json:"reason"`   // the employee's: why late (check-in) or early (check-out)
	PhotoID        *uint64    `json:"photo_id"` // nil: no photo (the Mini App no longer takes one)
	DistanceMeters *float64   `json:"distance_meters"`
	InsideOffice   *bool      `json:"inside_office"`
	Accuracy       *float64   `json:"accuracy"`
}

// Pending lists reviews the actor is allowed to act on.
func (s *Service) Pending(ctx context.Context, actor *auth.Actor, limit int) ([]PendingItem, error) {
	if !actor.IsReviewer() {
		return nil, apperr.ErrForbidden
	}
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	q := s.db.WithContext(ctx).Table("attendance_confirmations c").
		Select(`c.id AS confirmation_id, a.id AS attendance_id, c.type, e.id AS employee_id, e.employee_code, e.full_name,
			a.work_date, a.check_in_at, a.check_out_at, a.late_minutes, a.early_leave_minutes AS early_minutes,
			CASE WHEN c.type = 'CHECK_IN' THEN a.check_in_reason ELSE a.check_out_reason END AS reason,
			c.photo_id, l.distance_meters, l.inside_office, l.accuracy`).
		Joins("JOIN attendance a ON a.id = c.attendance_id").
		Joins("JOIN employees e ON e.id = a.employee_id").
		Joins("LEFT JOIN attendance_locations l ON l.id = c.location_id"). // shared with this submission
		Where("c.status = ?", models.ConfPending).
		Order("c.id ASC").Limit(limit)
	q = auth.ReviewScope(q, actor)
	items := []PendingItem{} // never null in JSON
	return items, q.Scan(&items).Error
}

// Get returns one attendance record with photos, locations and reviews, if the actor may see it.
func (s *Service) Get(ctx context.Context, actor *auth.Actor, id uint64) (*models.Attendance, error) {
	var att models.Attendance
	err := s.db.WithContext(ctx).Preload("Employee").Preload("Employee.Department").Preload("Employee.Branch").
		Preload("Employee.WorkSchedule").Preload("Photos").Preload("Locations", func(db *gorm.DB) *gorm.DB { return db.Order("id") }).
		Preload("Confirmations", func(db *gorm.DB) *gorm.DB { return db.Order("id") }).
		Preload("Confirmations.Reviewer", func(db *gorm.DB) *gorm.DB { return db.Select("id", "employee_code", "full_name") }).
		First(&att, id).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, apperr.NotFound("NOT_FOUND", "Attendance record not found.")
	}
	if err != nil {
		return nil, err
	}
	if !actor.CanView(att.Employee) {
		return nil, apperr.NotFound("NOT_FOUND", "Attendance record not found.")
	}
	return &att, nil
}

// Detail is one record with the day's plan: the hours the employee was expected
// to work (less a half-day leave), a holiday, and approved leave on the date.
type Detail struct {
	Attendance *models.Attendance
	Schedule   Day
	Holiday    string
	Leave      *models.LeaveRequest
}

// Detail returns Get plus the day's plan.
func (s *Service) Detail(ctx context.Context, actor *auth.Actor, id uint64) (*Detail, error) {
	att, err := s.Get(ctx, actor, id)
	if err != nil {
		return nil, err
	}
	out := &Detail{Attendance: att}
	plan, err := LoadPlanner(ctx, s.db, []uint64{att.EmployeeID}, att.WorkDate, att.WorkDate)
	if err != nil {
		return nil, err
	}
	out.Schedule = plan.Day(att.Employee, att.WorkDate)
	if _, name := s.isHoliday(ctx, att.Employee.BranchID, att.WorkDate); name != "" {
		out.Holiday = name
	}
	var lr models.LeaveRequest
	if s.db.WithContext(ctx).Preload("LeaveType").
		Where("employee_id = ? AND status = ? AND start_date <= ? AND end_date >= ?", att.EmployeeID, models.LeaveApproved, att.WorkDate, att.WorkDate).
		Order("id DESC").First(&lr).Error == nil {
		out.Leave = &lr
		if lr.IsHalfDay() {
			if st, err := s.settings.Get(ctx); err == nil {
				out.Schedule = out.Schedule.WithHalfOff(lr.DayType, att.WorkDate, st.Location)
			}
		}
	}
	return out, nil
}

// PhotoByID streams a photo after checking the actor may see its attendance record.
func (s *Service) PhotoByID(ctx context.Context, actor *auth.Actor, photoID uint64) (*models.AttendancePhoto, error) {
	var p models.AttendancePhoto
	if err := s.db.WithContext(ctx).First(&p, photoID).Error; err != nil {
		return nil, apperr.NotFound("NOT_FOUND", "Photo not found.")
	}
	if _, err := s.Get(ctx, actor, p.AttendanceID); err != nil {
		return nil, err
	}
	return &p, nil
}

func (s *Service) Store() storage.Store { return s.store }

type ListFilter struct {
	EmployeeID   uint64 `form:"employee_id"`
	DepartmentID uint64 `form:"department_id"`
	BranchID     uint64 `form:"branch_id"`
	From         string `form:"from"`
	To           string `form:"to"`
	Status       string `form:"status"`
	Confirmation string `form:"confirmation"`
	Page         int    `form:"page"`
	PageSize     int    `form:"page_size"`
}

type ListResult struct {
	Items    []models.Attendance `json:"items"`
	Total    int64               `json:"total"`
	Page     int                 `json:"page"`
	PageSize int                 `json:"page_size"`
}

func (s *Service) List(ctx context.Context, actor *auth.Actor, f ListFilter) (*ListResult, error) {
	scope, err := auth.Scope(ctx, s.db, actor)
	if err != nil {
		return nil, err
	}
	if f.Page < 1 {
		f.Page = 1
	}
	if f.PageSize < 1 || f.PageSize > 200 {
		f.PageSize = 10 // the default page of every list
	}
	q := s.db.WithContext(ctx).Model(&models.Attendance{}).Joins("JOIN employees e ON e.id = attendance.employee_id")
	if scope != nil {
		q = q.Where("attendance.employee_id IN ?", scope)
	}
	if f.EmployeeID != 0 {
		q = q.Where("attendance.employee_id = ?", f.EmployeeID)
	}
	if f.DepartmentID != 0 {
		q = q.Where("e.department_id = ?", f.DepartmentID)
	}
	if f.BranchID != 0 {
		q = q.Where("e.branch_id = ?", f.BranchID)
	}
	if f.From != "" {
		d, err := timeutil.ParseDate(f.From)
		if err != nil {
			return nil, apperr.BadRequest("INVALID_DATE", err.Error())
		}
		q = q.Where("attendance.work_date >= ?", d)
	}
	if f.To != "" {
		d, err := timeutil.ParseDate(f.To)
		if err != nil {
			return nil, apperr.BadRequest("INVALID_DATE", err.Error())
		}
		q = q.Where("attendance.work_date <= ?", d)
	}
	if f.Status != "" {
		q = q.Where("attendance.status = ?", strings.ToUpper(f.Status))
	}
	if f.Confirmation != "" {
		q = q.Where("attendance.confirmation_status = ?", strings.ToUpper(f.Confirmation))
	}
	res := &ListResult{Page: f.Page, PageSize: f.PageSize}
	if err := q.Count(&res.Total).Error; err != nil {
		return nil, err
	}
	// Reviews and locations let the list show the check-in and the check-out each with its own status.
	err = q.Preload("Employee").Preload("Employee.Branch").Preload("Confirmations").Preload("Locations").
		Order("attendance.work_date DESC, e.full_name ASC").
		Offset((f.Page - 1) * f.PageSize).Limit(f.PageSize).Find(&res.Items).Error
	return res, err
}
