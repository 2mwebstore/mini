// Package audit writes append-only audit records. Writes take the caller's
// transaction so an action and its audit entry commit (or roll back) together.
package audit

import (
	"context"
	"encoding/json"
	"time"

	"attendance/backend/internal/auth"
	"attendance/backend/internal/models"

	"gorm.io/gorm"
)

const (
	ActionCheckIn            = "attendance.check_in"
	ActionCheckOut           = "attendance.check_out"
	ActionConfirm            = "attendance.confirm"
	ActionReject             = "attendance.reject"
	ActionManualCreate       = "attendance.manual_create"
	ActionManualEdit         = "attendance.manual_edit"
	ActionAutoAbsent         = "attendance.auto_absent"
	ActionLeaveSubmit        = "leave.submit"
	ActionLeaveCancel        = "leave.cancel"
	ActionLeaveApprove       = "leave.approve"
	ActionLeaveReject        = "leave.reject"
	ActionEmployeeCreate     = "employee.create"
	ActionEmployeeUpdate     = "employee.update"
	ActionEmployeeDeactivate = "employee.deactivate"
	ActionTelegramLink       = "employee.telegram_link"
	ActionUserCreate         = "user.create"
	ActionSettingsUpdate     = "settings.update"
	ActionHolidayCreate      = "holiday.create"
	ActionHolidayDelete      = "holiday.delete"
	ActionShiftCreate        = "shift.create"
	ActionShiftUpdate        = "shift.update"
	ActionShiftDelete        = "shift.delete"
	ActionRosterUpdate       = "roster.update"
	ActionRosterCopy         = "roster.copy"
	ActionOverrideCreate     = "schedule_override.create"
	ActionOverrideUpdate     = "schedule_override.update"
	ActionOverrideDelete     = "schedule_override.delete"
	ActionReportExport       = "report.export"
	ActionLogin              = "auth.login"
	ActionLogout             = "auth.logout"
)

type Entry struct {
	Action           string
	EntityType       string
	EntityID         uint64
	TargetEmployeeID uint64
	Details          map[string]any
}

// Write records e. actor may be nil for system actions.
func Write(ctx context.Context, tx *gorm.DB, actor *auth.Actor, e Entry) error {
	row := models.AuditLog{
		Action:     e.Action,
		EntityType: e.EntityType,
		Channel:    auth.ChannelSystem,
		CreatedAt:  time.Now().UTC(),
	}
	if actor != nil {
		if id := actor.Employee.ID; id != 0 {
			row.ActorEmployeeID = &id
		}
		row.Channel = actor.Channel
		row.IP = actor.IP
	}
	if e.EntityID != 0 {
		v := e.EntityID
		row.EntityID = &v
	}
	if e.TargetEmployeeID != 0 {
		v := e.TargetEmployeeID
		row.TargetEmployeeID = &v
	}
	if e.Details != nil {
		b, err := json.Marshal(e.Details)
		if err != nil {
			return err
		}
		row.Details = b
	}
	return tx.WithContext(ctx).Create(&row).Error
}
