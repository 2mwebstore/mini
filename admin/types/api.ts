// API contracts for /api/v1 (see backend/internal/httpapi/handlers.go).
// Timestamps are UTC ISO strings; calendar dates are "YYYY-MM-DDT00:00:00Z"
// values that represent a day in the company timezone.

export type Permission =
  | 'attendance.self' | 'leave.self' | 'team.view' | 'team.review' | 'all.view' | 'all.review'
  | 'attendance.edit' | 'staff.manage' | 'report.export' | 'settings.view' | 'settings.edit'
  | 'audit.view' | 'roles.assign_admin' | 'branch.view' | 'branch.review' | 'branch.settings' | 'branches.manage'
  | 'schedules.manage'

export type RoleCode = 'super_admin' | 'hr_admin' | 'branch_admin' | 'manager' | 'staff'
export type EmployeeStatus = 'ACTIVE' | 'INACTIVE' | 'SUSPENDED'
export type AttendanceType = 'CHECK_IN' | 'CHECK_OUT'
export type ConfirmationStatus = 'PENDING' | 'CONFIRMED' | 'REJECTED' | 'SUPERSEDED'
export type DayStatus =
  | 'PRESENT' | 'LATE' | 'EARLY_LEAVE' | 'ABSENT' | 'LEAVE' | 'HOLIDAY' | 'WEEKEND' | 'NOT_CHECKED_IN'
export type LeaveStatus = 'PENDING' | 'APPROVED' | 'REJECTED' | 'CANCELLED'

export interface Branch {
  id: number
  code: string
  name: string
  address: string
  admin_employee_id: number | null
  office_latitude: number | null
  office_longitude: number | null
  allowed_radius_meters: number | null
  telegram_group_id: number
  telegram_review_chat_id: number
  status: 'ACTIVE' | 'INACTIVE'
  staff_count: number
  admin?: Employee
}

/** Owner: any field. A branch's own admin: office location and Telegram chats only. */
export interface BranchInput {
  code?: string
  name?: string
  address?: string
  status?: 'ACTIVE' | 'INACTIVE'
  office_latitude?: number
  office_longitude?: number
  allowed_radius_meters?: number
  use_company_office?: boolean
  telegram_group_id?: number
  telegram_review_chat_id?: number
}

export interface Department {
  id: number
  name: string
}

/** NORMAL ends the same day; CROSS_DAY is a night shift ending the next day (22:00–06:00). */
export type WorkType = 'NORMAL' | 'CROSS_DAY'

/** A shift (work schedule): hours, grace period and usual work days. */
export interface WorkSchedule {
  id: number
  name: string
  start_time: string
  end_time: string
  work_type: WorkType
  grace_minutes: number
  work_days: string // "1,2,3,4,5" (ISO weekdays, 1 = Monday)
  color: string
  is_default: boolean
  employees?: number // people whose usual shift it is
}

export interface ShiftInput {
  name?: string
  start_time?: string
  end_time?: string
  work_type?: WorkType
  grace_minutes?: number
  work_days?: number[]
  color?: string
  is_default?: boolean
}

/** What someone works on one date: special schedule > roster > usual shift. */
export interface PlanDay {
  work: boolean
  start?: string
  end?: string
  grace_minutes: number
  cross_day: boolean
  shift_id?: number
  shift_name?: string
  color?: string
  source: 'DEFAULT' | 'ROSTER' | 'SPECIAL'
  override_id?: number
  note?: string
}

export interface RosterCell extends PlanDay {
  date: string
  holiday?: string
  leave?: string
}

export interface RosterRow {
  employee_id: number
  employee_code: string
  full_name: string
  branch: string
  department: string
  usual_shift: string
  can_edit: boolean
  cells: RosterCell[]
}

export interface Roster {
  from: string
  to: string
  days: string[]
  rows: RosterRow[]
  shifts: WorkSchedule[]
}

export interface RosterItem {
  employee_id: number
  date: string
  work_schedule_id?: number
  off?: boolean
  reset?: boolean
}

export interface RosterQuery {
  from?: string
  to?: string
  branch_id?: string
  department_id?: string
  q?: string
}

export type OverrideKind = 'SHIFT' | 'CUSTOM' | 'OFF'

/** A special / temporary schedule for a date range. */
export interface ScheduleOverride {
  id: number
  employee_id: number
  start_date: string
  end_date: string
  kind: OverrideKind
  work_schedule_id: number | null
  start_time: string | null
  end_time: string | null
  work_type: WorkType | null
  grace_minutes: number | null
  work_days: string
  reason: string
  created_at: string
  employee?: Employee
  work_schedule?: WorkSchedule
}

export interface OverrideInput {
  employee_id: number
  start_date: string
  end_date: string
  kind: OverrideKind
  work_schedule_id?: number
  start_time?: string
  end_time?: string
  work_type?: WorkType
  grace_minutes?: number
  work_days?: number[]
  reason: string
}

export interface Employee {
  id: number
  employee_code: string
  full_name: string
  department_id: number | null
  branch_id: number | null
  position: string
  phone: string
  email: string
  join_date: string | null
  work_schedule_id: number | null
  manager_id: number | null
  role_id: number
  status: EmployeeStatus
  created_at: string
  deleted_at?: string | null
  department?: Department
  branch?: Branch
  has_login: boolean
  work_schedule?: WorkSchedule
  role?: { id: number; code: RoleCode; name: string }
  telegram_account?: { telegram_user_id: number; username: string; first_name: string; last_name: string }
}

/** Create/update body. Omitted fields are left unchanged on update. */
export interface EmployeeInput {
  employee_code?: string
  full_name?: string
  telegram_user_id?: number
  department?: string // by name; created if missing
  department_id?: number // 0 clears
  branch_id?: number
  position?: string
  phone?: string
  email?: string
  join_date?: string
  work_schedule_id?: number
  manager_id?: number // 0 clears
  role?: RoleCode
  status?: EmployeeStatus
}

export interface MeResponse {
  employee: Employee
  role: RoleCode
  permissions: Permission[]
  company: { name: string; timezone: string }
}

export interface AttendancePhoto {
  id: number
  type: AttendanceType
  created_at: string
  url: string
}

export interface AttendanceLocation {
  id: number
  type: AttendanceType
  latitude: number
  longitude: number
  accuracy: number | null
  distance_meters: number | null
  inside_office: boolean | null
  created_at: string
}

export interface Confirmation {
  id: number
  type: AttendanceType
  status: ConfirmationStatus
  reviewed_by: number | null
  reviewed_at: string | null
  reason: string
  created_at: string
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
  status: DayStatus
  confirmation_status: ConfirmationStatus
  source: 'TELEGRAM' | 'MANUAL' | 'SYSTEM'
  note: string
  employee?: Employee
  locations?: AttendanceLocation[]
  confirmations?: Confirmation[]
}

export interface AttendanceDetail {
  attendance: Attendance
  photos: AttendancePhoto[]
}

export interface Page<T> {
  items: T[]
  total: number
  page: number
  page_size: number
}

export interface AttendanceFilter {
  from?: string
  to?: string
  employee_id?: number
  department_id?: number
  branch_id?: number
  status?: string
  confirmation?: string
  page?: number
  page_size?: number
}

export interface ManualAttendanceInput {
  employee_id: number
  date: string
  check_in?: string // HH:MM company time
  check_out?: string
  status?: string
  reason: string
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
  photo_id: number | null
}

export interface LeaveType {
  id: number
  code: string
  name: string
}

export interface LeaveRequest {
  id: number
  employee_id: number
  start_date: string
  end_date: string
  days: number
  reason: string
  status: LeaveStatus
  reviewed_at: string | null
  review_note: string
  created_at: string
  employee?: Employee
  leave_type?: LeaveType
  reviewer?: Employee
}

export interface DayRow {
  attendance_id?: number
  employee_id: number
  employee_code: string
  full_name: string
  department: string
  branch_id?: number
  branch: string
  date: string
  check_in_at: string | null
  check_out_at: string | null
  working_minutes: number
  late_minutes: number
  early_leave_minutes: number
  status: DayStatus
  confirmation_status: ConfirmationStatus | ''
  confirmed_by: string
  leave_type: string
  shift?: string // the day's scheduled shift (absent on days off)
  shift_start?: string
  shift_end?: string
  cross_day?: boolean
}

export interface Summary {
  employee_id: number
  employee_code: string
  full_name: string
  department: string
  branch: string
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

export interface Report {
  from: string
  to: string
  rows: DayRow[]
  summaries: Summary[]
  totals: Summary
  staff_count: number
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

export interface CompanySettings {
  company_name: string
  timezone: string
  confirmation_required: boolean
  gps_enabled: boolean
  gps_required: boolean
  gps_reject_outside: boolean
  office_latitude: number
  office_longitude: number
  allowed_radius_meters: number
  daily_report_enabled: boolean
  daily_report_time: string
  missing_checkin_alert_time: string
  day_close_time: string
  // Telegram (the bot token itself is write-only)
  telegram_bot_token_set: boolean
  telegram_bot_username: string
  miniapp_url: string
  telegram_group_id: number
  telegram_review_chat_id: number
}

/** Editable keys; telegram_bot_token is write-only ("" clears it). */
export type SettingsUpdate = Partial<Omit<CompanySettings, 'telegram_bot_token_set' | 'telegram_bot_username'>> & { telegram_bot_token?: string }

export interface BackupStatus {
  ok: boolean
  at: string
  file?: string
  size?: string
}

export interface SettingsResponse {
  settings: CompanySettings
  backup: BackupStatus | null
}

export interface Holiday {
  id: number
  date: string
  name: string
}

export interface AuditLog {
  id: number
  actor_employee_id: number | null
  channel: string
  action: string
  entity_type: string
  entity_id: number | null
  target_employee_id: number | null
  details: Record<string, unknown> | null
  ip: string
  created_at: string
  actor?: Employee
  target?: Employee
}

export interface Items<T> {
  items: T[]
}
