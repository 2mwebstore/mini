// Package models defines GORM models mapped 1:1 to the SQL migrations.
// Calendar dates (work_date, leave dates, holidays) are time.Time values at
// 00:00 UTC that represent a date in the company timezone. Timestamps are UTC.
package models

import (
	"encoding/json"
	"time"
)

const (
	RoleSuperAdmin  uint8 = 1
	RoleHRAdmin     uint8 = 2
	RoleManager     uint8 = 3
	RoleStaff       uint8 = 4
	RoleBranchAdmin uint8 = 5
)

const (
	EmployeeActive    = "ACTIVE"
	EmployeeInactive  = "INACTIVE"
	EmployeeSuspended = "SUSPENDED"
)

// Attendance status (what happened that day).
const (
	StatusPresent    = "PRESENT"
	StatusLate       = "LATE"
	StatusEarlyLeave = "EARLY_LEAVE"
	StatusAbsent     = "ABSENT"
	StatusLeave      = "LEAVE"
	StatusHoliday    = "HOLIDAY"
	StatusWeekend    = "WEEKEND"
)

// Confirmation status (has an admin reviewed it).
const (
	ConfPending    = "PENDING"
	ConfConfirmed  = "CONFIRMED"
	ConfRejected   = "REJECTED"
	ConfSuperseded = "SUPERSEDED" // a rejected check-in that the employee re-submitted
)

const (
	TypeCheckIn  = "CHECK_IN"
	TypeCheckOut = "CHECK_OUT"
)

const (
	SourceTelegram = "TELEGRAM"
	SourceManual   = "MANUAL"
	SourceSystem   = "SYSTEM"
)

const (
	LeavePending   = "PENDING"
	LeaveApproved  = "APPROVED"
	LeaveRejected  = "REJECTED"
	LeaveCancelled = "CANCELLED"
)

type Role struct {
	ID          uint8  `gorm:"primaryKey" json:"id"`
	Code        string `json:"code"`
	Name        string `json:"name"`
	Description string `json:"description"`
}

type Department struct {
	ID        uint64    `gorm:"primaryKey" json:"id"`
	Name      string    `json:"name"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

// WorkSchedule is a shift: hours, grace period and usual work days.
type WorkSchedule struct {
	ID           uint64    `gorm:"primaryKey" json:"id"`
	Name         string    `json:"name"`
	StartTime    string    `json:"start_time"`
	EndTime      string    `json:"end_time"`
	WorkType     string    `json:"work_type"` // WorkNormal | WorkCrossDay
	GraceMinutes int       `json:"grace_minutes"`
	WorkDays     string    `json:"work_days"`
	Color        string    `json:"color"`
	IsDefault    bool      `json:"is_default"`
	CreatedAt    time.Time `json:"created_at"`
	UpdatedAt    time.Time `json:"updated_at"`
}

// Shift work types.
const (
	WorkNormal   = "NORMAL"    // ends the same day (08:00-17:00)
	WorkCrossDay = "CROSS_DAY" // night shift ending the next day (22:00-06:00)
)

// RosterShift is the weekly roster's entry for one employee and date
// (WorkScheduleID nil = day off).
type RosterShift struct {
	ID             uint64        `gorm:"primaryKey" json:"id"`
	EmployeeID     uint64        `json:"employee_id"`
	WorkDate       time.Time     `gorm:"type:date" json:"work_date"`
	WorkScheduleID *uint64       `json:"work_schedule_id"`
	UpdatedBy      *uint64       `json:"updated_by"`
	CreatedAt      time.Time     `json:"created_at"`
	UpdatedAt      time.Time     `json:"updated_at"`
	WorkSchedule   *WorkSchedule `gorm:"foreignKey:WorkScheduleID" json:"work_schedule,omitempty"`
}

// ScheduleOverride is a special (temporary) schedule for a date range.
type ScheduleOverride struct {
	ID             uint64        `gorm:"primaryKey" json:"id"`
	EmployeeID     uint64        `json:"employee_id"`
	StartDate      time.Time     `gorm:"type:date" json:"start_date"`
	EndDate        time.Time     `gorm:"type:date" json:"end_date"`
	Kind           string        `json:"kind"` // OverrideShift | OverrideCustom | OverrideOff
	WorkScheduleID *uint64       `json:"work_schedule_id"`
	StartTime      *string       `json:"start_time"`
	EndTime        *string       `json:"end_time"`
	WorkType       *string       `json:"work_type"`
	GraceMinutes   *int          `json:"grace_minutes"`
	WorkDays       string        `json:"work_days"`
	Reason         string        `json:"reason"`
	CreatedBy      *uint64       `json:"created_by"`
	CreatedAt      time.Time     `json:"created_at"`
	UpdatedAt      time.Time     `json:"updated_at"`
	Employee       *Employee     `gorm:"foreignKey:EmployeeID" json:"employee,omitempty"`
	WorkSchedule   *WorkSchedule `gorm:"foreignKey:WorkScheduleID" json:"work_schedule,omitempty"`
}

// Special schedule kinds.
const (
	OverrideShift  = "SHIFT"  // work another shift
	OverrideCustom = "CUSTOM" // work custom hours
	OverrideOff    = "OFF"    // do not work
)

// Branch is a company location. Each has at most one branch admin.
type Branch struct {
	ID                   uint64    `gorm:"primaryKey" json:"id"`
	Code                 string    `json:"code"`
	Name                 string    `json:"name"`
	Address              string    `json:"address"`
	AdminEmployeeID      *uint64   `json:"admin_employee_id"`
	OfficeLatitude       *float64  `json:"office_latitude"`
	OfficeLongitude      *float64  `json:"office_longitude"`
	AllowedRadiusMeters  *float64  `json:"allowed_radius_meters"`
	TelegramGroupID      int64     `json:"telegram_group_id"`
	TelegramReviewChatID int64     `json:"telegram_review_chat_id"`
	Status               string    `json:"status"`
	CreatedAt            time.Time `json:"created_at"`
	UpdatedAt            time.Time `json:"updated_at"`

	Admin      *Employee `gorm:"foreignKey:AdminEmployeeID" json:"admin,omitempty"`
	StaffCount int64     `gorm:"-" json:"staff_count"`
}

// HasOffice reports whether the branch overrides the company office location.
func (b *Branch) HasOffice() bool {
	return b != nil && b.OfficeLatitude != nil && b.OfficeLongitude != nil && b.AllowedRadiusMeters != nil
}

type Employee struct {
	ID             uint64     `gorm:"primaryKey" json:"id"`
	EmployeeCode   string     `json:"employee_code"`
	FullName       string     `json:"full_name"`
	DepartmentID   *uint64    `json:"department_id"`
	BranchID       *uint64    `json:"branch_id"`
	Position       string     `json:"position"`
	Phone          string     `json:"phone"`
	Email          string     `json:"email"`
	JoinDate       *time.Time `json:"join_date"`
	WorkScheduleID *uint64    `json:"work_schedule_id"`
	ManagerID      *uint64    `json:"manager_id"`
	RoleID         uint8      `json:"role_id"`
	Status         string     `json:"status"`
	CreatedAt      time.Time  `json:"created_at"`
	UpdatedAt      time.Time  `json:"updated_at"`
	DeletedAt      *time.Time `json:"deleted_at,omitempty"`

	Department      *Department      `gorm:"foreignKey:DepartmentID" json:"department,omitempty"`
	Branch          *Branch          `gorm:"foreignKey:BranchID" json:"branch,omitempty"`
	WorkSchedule    *WorkSchedule    `gorm:"foreignKey:WorkScheduleID" json:"work_schedule,omitempty"`
	Role            *Role            `gorm:"foreignKey:RoleID" json:"role,omitempty"`
	TelegramAccount *TelegramAccount `gorm:"foreignKey:EmployeeID" json:"telegram_account,omitempty"`
	HasLogin        bool             `gorm:"-" json:"has_login"` // has an admin panel login
}

// CompanyAdminRoles lists the company-wide admin roles for SQL "IN ?" filters
// (as []int: a []uint8 would be sent as a single binary string).
var CompanyAdminRoles = []int{int(RoleSuperAdmin), int(RoleHRAdmin)}

// IsCompanyAdmin: super and HR admins work across all branches (they have no branch).
func IsCompanyAdmin(roleID uint8) bool { return roleID == RoleSuperAdmin || roleID == RoleHRAdmin }

// IsAdminRole: roles only a super admin may grant or modify.
func IsAdminRole(roleID uint8) bool { return IsCompanyAdmin(roleID) || roleID == RoleBranchAdmin }

func (e *Employee) IsActive() bool { return e.Status == EmployeeActive && e.DeletedAt == nil }

type TelegramAccount struct {
	ID             uint64    `gorm:"primaryKey" json:"id"`
	EmployeeID     uint64    `json:"employee_id"`
	TelegramUserID int64     `json:"telegram_user_id"`
	Username       string    `json:"username"`
	FirstName      string    `json:"first_name"`
	LastName       string    `json:"last_name"`
	LinkedAt       time.Time `json:"linked_at"`
	UpdatedAt      time.Time `json:"updated_at"`
}

type User struct {
	ID           uint64     `gorm:"primaryKey" json:"id"`
	EmployeeID   uint64     `json:"employee_id"`
	Email        string     `json:"email"`
	PasswordHash string     `json:"-"`
	Status       string     `json:"status"`
	LastLoginAt  *time.Time `json:"last_login_at"`
	CreatedAt    time.Time  `json:"created_at"`
	UpdatedAt    time.Time  `json:"updated_at"`
	Employee     *Employee  `gorm:"foreignKey:EmployeeID" json:"employee,omitempty"`
}

type Holiday struct {
	ID        uint64    `gorm:"primaryKey" json:"id"`
	Date      time.Time `gorm:"type:date" json:"date"`
	Name      string    `json:"name"`
	CreatedAt time.Time `json:"created_at"`
}

type Attendance struct {
	ID                 uint64     `gorm:"primaryKey" json:"id"`
	EmployeeID         uint64     `json:"employee_id"`
	WorkDate           time.Time  `gorm:"type:date" json:"work_date"`
	CheckInAt          *time.Time `json:"check_in_at"`
	CheckOutAt         *time.Time `json:"check_out_at"`
	WorkingMinutes     int        `json:"working_minutes"`
	LateMinutes        int        `json:"late_minutes"`
	EarlyLeaveMinutes  int        `json:"early_leave_minutes"`
	Status             string     `json:"status"`
	ConfirmationStatus string     `json:"confirmation_status"`
	ConfirmedBy        *uint64    `json:"confirmed_by"`
	ConfirmedAt        *time.Time `json:"confirmed_at"`
	Source             string     `json:"source"`
	Note               string     `json:"note"`
	CreatedAt          time.Time  `json:"created_at"`
	UpdatedAt          time.Time  `json:"updated_at"`

	Employee      *Employee                `gorm:"foreignKey:EmployeeID" json:"employee,omitempty"`
	Photos        []AttendancePhoto        `gorm:"foreignKey:AttendanceID" json:"photos,omitempty"`
	Locations     []AttendanceLocation     `gorm:"foreignKey:AttendanceID" json:"locations,omitempty"`
	Confirmations []AttendanceConfirmation `gorm:"foreignKey:AttendanceID" json:"confirmations,omitempty"`
}

func (Attendance) TableName() string { return "attendance" }

type AttendancePhoto struct {
	ID                   uint64    `gorm:"primaryKey" json:"id"`
	AttendanceID         uint64    `json:"attendance_id"`
	Type                 string    `json:"type"`
	TelegramFileID       string    `json:"-"`
	TelegramFileUniqueID string    `json:"-"`
	ContentSHA256        string    `gorm:"column:content_sha256" json:"-"`
	TelegramUserID       int64     `json:"telegram_user_id"`
	StorageKey           string    `json:"-"`
	StorageURL           string    `json:"-"`
	ContentType          string    `json:"content_type"`
	SizeBytes            int64     `json:"size_bytes"`
	CreatedAt            time.Time `json:"created_at"`
}

type AttendanceLocation struct {
	ID             uint64    `gorm:"primaryKey" json:"id"`
	AttendanceID   uint64    `json:"attendance_id"`
	Type           string    `json:"type"`
	Latitude       float64   `json:"latitude"`
	Longitude      float64   `json:"longitude"`
	Accuracy       *float64  `json:"accuracy"`
	DistanceMeters *float64  `json:"distance_meters"`
	InsideOffice   *bool     `json:"inside_office"`
	CreatedAt      time.Time `json:"created_at"`
}

type AttendanceConfirmation struct {
	ID           uint64     `gorm:"primaryKey" json:"id"`
	AttendanceID uint64     `json:"attendance_id"`
	Type         string     `json:"type"`
	Status       string     `json:"status"`
	ReviewedBy   *uint64    `json:"reviewed_by"`
	ReviewedAt   *time.Time `json:"reviewed_at"`
	Reason       string     `json:"reason"`
	CreatedAt    time.Time  `json:"created_at"`
	UpdatedAt    time.Time  `json:"updated_at"`
}

type LeaveType struct {
	ID        uint64 `gorm:"primaryKey" json:"id"`
	Code      string `json:"code"`
	Name      string `json:"name"`
	Active    bool   `json:"active"`
	SortOrder int    `json:"sort_order"`
}

type LeaveRequest struct {
	ID          uint64     `gorm:"primaryKey" json:"id"`
	EmployeeID  uint64     `json:"employee_id"`
	LeaveTypeID uint64     `json:"leave_type_id"`
	StartDate   time.Time  `gorm:"type:date" json:"start_date"`
	EndDate     time.Time  `gorm:"type:date" json:"end_date"`
	Days        int        `json:"days"`
	Reason      string     `json:"reason"`
	Status      string     `json:"status"`
	ReviewedBy  *uint64    `json:"reviewed_by"`
	ReviewedAt  *time.Time `json:"reviewed_at"`
	ReviewNote  string     `json:"review_note"`
	CreatedAt   time.Time  `json:"created_at"`
	UpdatedAt   time.Time  `json:"updated_at"`

	Employee  *Employee  `gorm:"foreignKey:EmployeeID" json:"employee,omitempty"`
	LeaveType *LeaveType `gorm:"foreignKey:LeaveTypeID" json:"leave_type,omitempty"`
	Reviewer  *Employee  `gorm:"foreignKey:ReviewedBy" json:"reviewer,omitempty"`
}

type AuditLog struct {
	ID               uint64          `gorm:"primaryKey" json:"id"`
	ActorEmployeeID  *uint64         `json:"actor_employee_id"`
	Channel          string          `json:"channel"`
	Action           string          `json:"action"`
	EntityType       string          `json:"entity_type"`
	EntityID         *uint64         `json:"entity_id"`
	TargetEmployeeID *uint64         `json:"target_employee_id"`
	Details          json.RawMessage `json:"details"`
	IP               string          `json:"ip"`
	CreatedAt        time.Time       `json:"created_at"`
	Actor            *Employee       `gorm:"foreignKey:ActorEmployeeID" json:"actor,omitempty"`
	Target           *Employee       `gorm:"foreignKey:TargetEmployeeID" json:"target,omitempty"`
}

type Notification struct {
	ID                  uint64 `gorm:"primaryKey"`
	RecipientChatID     int64
	RecipientEmployeeID *uint64
	Type                string
	RefType             string
	RefID               *uint64
	Status              string
	TelegramMessageID   *int64
	Error               string
	CreatedAt           time.Time
}

type CompanySetting struct {
	Key       string `gorm:"primaryKey;column:key"`
	Value     string
	UpdatedBy *uint64
	UpdatedAt time.Time
}
