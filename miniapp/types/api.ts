// API contracts for /api/v1/app (see backend/internal/httpapi/app.go).
// Timestamps are UTC ISO strings; calendar dates (work_date, leave dates) are
// "YYYY-MM-DDT00:00:00Z" values that represent a day in the company timezone.

export type AttendanceType = 'CHECK_IN' | 'CHECK_OUT'
export type ConfirmationStatus = 'PENDING' | 'CONFIRMED' | 'REJECTED' | 'SUPERSEDED'
export type DayStatus =
  | 'PRESENT' | 'LATE' | 'EARLY_LEAVE' | 'ABSENT' | 'LEAVE' | 'HOLIDAY' | 'WEEKEND' | 'NOT_CHECKED_IN'
export type LeaveStatus = 'PENDING' | 'APPROVED' | 'REJECTED' | 'CANCELLED'

export interface Me {
  employee_id: number
  employee_code: string
  full_name: string
  is_system_user: boolean // an admin account: no check-in or leave, only the Team tab
  role: 'super_admin' | 'hr_admin' | 'branch_admin' | 'manager' | 'staff'
  is_reviewer: boolean
  is_admin: boolean
  can_export: boolean
  can_manage_staff: boolean
  branch_id: number | null
  branch_name: string
  permissions: string[]
}

export interface Profile {
  employee_code: string
  full_name: string
  position: string
  status: string
  department: string
  branch: string
  role_name: string
  schedule: { name: string; start: string; end: string; cross_day: boolean; grace_minutes: number; work_days: number[] }
}

export interface CompanySettings {
  company_name: string
  timezone: string
  confirmation_required: boolean // GPS rules are per branch: see Precheck
}

export interface MeResponse {
  me: Me
  profile: Profile
  settings: CompanySettings
}

export interface Confirmation {
  id: number
  type: AttendanceType
  status: ConfirmationStatus
  reason: string
  reviewed_at: string | null
  photo_id: number | null
  location_id: number | null
}

export interface Attendance {
  id: number
  employee_id: number
  work_date: string
  check_in_at: string | null
  check_out_at: string | null
  working_minutes: number
  late_minutes: number
  early_leave_minutes: number
  check_in_reason?: string // why late, in the employee's words
  check_out_reason?: string // why leaving early
  status: DayStatus
  confirmation_status: ConfirmationStatus
  note: string
  confirmations?: Confirmation[]
  employee?: { id: number; employee_code: string; full_name: string }
}

export interface Precheck {
  allowed: boolean
  need_gps: boolean
  gps_required: boolean
  message?: string
  late_minutes?: number // right now: how late a check-in would be…
  early_minutes?: number // …or how early a check-out
  reason_required: boolean // late or early: say why
}

export interface TodayStatus {
  date: string
  work_day: boolean
  holiday?: string
  schedule_start: string
  schedule_end: string
  cross_day: boolean // the shift ends the next day (night shift); date is the day it started
  shift_name: string
  special?: string // reason of a special schedule in force
  grace_minutes: number
  attendance: Attendance | null
  check_in: Precheck
  check_out: Precheck
  half_leave?: HalfLeave // approved half-day leave: the schedule shown is the half still worked
}

export interface HalfLeave {
  day_type: HalfDay
  from: string
  to: string
  leave_type: string
  shift_start: string // the whole shift's start and type, for naming the half
  work_type: 'NORMAL' | 'CROSS_DAY'
}

export interface SubmitResult {
  type: AttendanceType
  attendance: Attendance
  pending: boolean
  resubmission: boolean
  location_label: string
}

export interface DayRow {
  attendance_id?: number
  employee_id: number
  employee_code: string
  full_name: string
  department: string
  date: string
  check_in_at: string | null
  check_out_at: string | null
  working_minutes: number
  late_minutes: number
  early_leave_minutes: number
  check_in_reason?: string
  check_out_reason?: string
  status: DayStatus
  confirmation_status: ConfirmationStatus | ''
  confirmed_by: string
  leave_type: string
  leave_half?: HalfDay // half the day was leave ("Annual Leave (first half)" in leave_type)
}

export interface Summary {
  working_days: number
  present_days: number
  late_days: number
  early_leave_days: number
  absent_days: number
  leave_days: number
  pending_days: number
  rejected_days: number
  missing_checkout: number
  late_minutes: number
  working_minutes: number
  avg_minutes_per_day: number
}

export interface HistoryResponse {
  from: string
  to: string
  rows: DayRow[]
  summary: Summary | null
}

export interface LeaveType {
  id: number
  code: string
  name: string
  annual_days: number // yearly allowance; 0 = not counted
  approval: 'MANUAL' | 'AUTO' // AUTO: approved as soon as it is submitted
  allow_exceed: boolean
  max_days_per_request: number
  min_notice_days: number
  color: string
  description: string
}

/** One leave type's balance for the year (days count in the year a request starts). */
export interface LeaveBalance {
  leave_type_id: number
  code: string
  name: string
  color: string
  year: number
  tracked: boolean // false: no allowance (not counted)
  allocated: number
  carried: number
  used: number
  pending: number
  remaining: number // allocated + carried − used
  available: number // remaining − pending
  custom: boolean
}

export type HalfDay = 'FIRST_HALF' | 'SECOND_HALF'
export type DayType = 'FULL' | HalfDay

export interface LeaveRequest {
  id: number
  employee_id: number
  leave_type_id: number
  start_date: string
  end_date: string
  day_type: DayType
  half_from: string | null // a half day's hours (company time)
  half_to: string | null
  days: number // 0.5 for a half day
  reason: string
  status: LeaveStatus
  review_note: string
  created_at: string
  employee?: { id: number; employee_code: string; full_name: string }
  leave_type?: LeaveType
  reviewer?: { full_name: string }
}

export interface LeaveInput {
  leave_type_id: number
  start_date: string // YYYY-MM-DD
  end_date: string
  day_type: DayType
  reason: string
}

/** The employee's shift on the first requested date: what a half day means for them. */
export interface DayPlan {
  date: string
  work: boolean
  holiday: boolean
  shift_name?: string
  start?: string
  end?: string
  work_type: 'NORMAL' | 'CROSS_DAY'
  first_half: [string, string]
  second_half: [string, string]
  second_half_next_day: boolean
  cross_days: boolean // a night shift falls in the range
}

export interface LeavePreview {
  days: number // 0 when there is a problem
  plan: DayPlan
  balance?: LeaveBalance | null
  problem: { code: string; message: string } | null // why the request would be refused
  half_from?: string | null
  half_to?: string | null
}

export interface PendingAttendance {
  confirmation_id: number
  attendance_id: number
  type: AttendanceType
  employee_id: number
  employee_code: string
  full_name: string
  work_date: string
  check_in_at: string | null
  check_out_at: string | null
  late_minutes: number
  early_leave_minutes: number
  reason: string // theirs: why late (check-in) or early (check-out)
  photo_id: number | null // none: check-ins have no photo
  distance_meters: number | null
  inside_office: boolean | null
  accuracy: number | null
}

/** Field names come from the Go struct as-is (no JSON tags). */
export interface DayCounts {
  Staff: number
  Confirmed: number
  Pending: number
  Rejected: number
  Leave: number
  Absent: number
  NotYet: number
  Late: number
  MissingOut: number
}

export interface DailyReport {
  date: string
  counts: DayCounts
  rows: DayRow[]
}

export interface Items<T> {
  items: T[]
}

/** Extra time an employee asks to work on a date (statuses as leave). */
export interface OvertimeRequest {
  id: number
  employee_id: number
  work_date: string
  start_at: string
  end_at: string // may be the next day
  minutes: number
  reason: string
  status: LeaveStatus
  review_note: string
  reviewed_at: string | null
  created_at: string
  employee?: { id: number; employee_code: string; full_name: string; branch?: { name: string } }
  reviewer?: { full_name: string }
}

/** The person's shift on a date and the suggested overtime: from its end, for an hour. */
export interface OvertimePlan {
  date: string
  work: boolean // a work day (else the usual shift's hours)
  shift_name: string
  shift_start: string
  shift_end: string
  cross_day: boolean
  default_from: string
  default_to: string
}

export interface OvertimeInput {
  date: string
  start_time: string
  end_time: string // earlier than the start = the next day
  reason: string
}
