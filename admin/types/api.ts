// API contracts for /api/v1 (see backend/internal/httpapi/handlers.go).
// Timestamps are UTC ISO strings; calendar dates are "YYYY-MM-DDT00:00:00Z"
// values that represent a day in the company timezone.

export type Permission =
  | 'attendance.self' | 'leave.self' | 'team.view' | 'team.review' | 'all.view' | 'all.review'
  | 'attendance.edit' | 'staff.manage' | 'report.export' | 'settings.view' | 'settings.edit'
  | 'audit.view' | 'roles.assign_admin' | 'branch.view' | 'branch.review' | 'branch.settings' | 'branches.manage' | 'branches.create'
  | 'schedules.manage' | 'leave.manage' | 'users.manage' | 'users.branch'

/** Built-in: super_admin, hr_admin, branch_admin, manager, staff — plus roles created in Role management. */
export type RoleCode = string
export type EmployeeStatus = 'ACTIVE' | 'INACTIVE' | 'SUSPENDED'
export type AttendanceType = 'CHECK_IN' | 'CHECK_OUT'
export type ConfirmationStatus = 'PENDING' | 'CONFIRMED' | 'REJECTED' | 'SUPERSEDED'
export type DayStatus =
  | 'PRESENT' | 'LATE' | 'EARLY_LEAVE' | 'ABSENT' | 'LEAVE' | 'HOLIDAY' | 'WEEKEND' | 'NOT_CHECKED_IN'
export type LeaveStatus = 'PENDING' | 'APPROVED' | 'REJECTED' | 'CANCELLED'

/** A branch runs on its own: its Telegram bot and chats, GPS rules and office. */
export interface Branch {
  id: number
  code: string
  name: string
  address: string
  admin_employee_id: number | null
  office_latitude: number | null
  office_longitude: number | null
  allowed_radius_meters: number | null
  gps_enabled: boolean | null // null = off
  gps_required: boolean | null
  gps_reject_outside: boolean | null
  telegram_bot_set: boolean // the token itself is write-only
  telegram_bot_username: string
  miniapp_url: string
  telegram_group_id: number
  telegram_review_chat_id: number
  status: 'ACTIVE' | 'INACTIVE'
  staff_count: number
  admin?: Employee
}

/** Owner / branch creators: any field. A branch's own admin: office, GPS and Telegram only. */
export interface BranchInput {
  code?: string
  name?: string
  address?: string
  status?: 'ACTIVE' | 'INACTIVE'
  office_latitude?: number
  office_longitude?: number
  allowed_radius_meters?: number
  clear_office?: boolean
  gps?: { enabled: boolean; required?: boolean; reject_outside?: boolean }
  telegram_bot_token?: string // write-only; "" removes the bot
  miniapp_url?: string
  telegram_group_id?: number
  telegram_review_chat_id?: number
  starter?: boolean // create: start with a default shift and the usual leave types
}

export interface Department {
  id: number
  branch_id: number
  branch_name: string
  name: string
  employees?: number
}

/** A department or position of one branch. */
export interface OrgItem {
  id: number
  branch_id: number
  branch_name: string
  name: string
  employees: number
}

// ---------- access: roles, system users, branch access ----------

export interface PermissionInfo {
  code: Permission
  group: string
  label: string
  description: string
}

export interface Role {
  id: number
  code: RoleCode
  name: string
  description: string
  is_system: boolean
  permissions: Permission[]
  employees: number
  users: number
  locked: boolean // the owner role: always every permission
  assignable: boolean // the signed-in user may give this role to a system user
  editable: boolean // the signed-in user may change (and delete) it
  branch_id: number | null // a branch's own role (null: a company role)
  branch_name: string
}

export interface RoleInput {
  name?: string
  description?: string
  permissions?: Permission[]
  branch_id?: number // create: the branch it belongs to (0 = a company role, owner only)
}

/** An admin panel account. System users are not employees: they don't check in. */
export interface SystemUser {
  id: number // the login
  person_id: number // used for branch admin assignment and reviews
  full_name: string
  email: string
  phone: string
  telegram_user_id: number | null // optional: review notifications and the Mini App Team tab
  status: 'ACTIVE' | 'INACTIVE'
  last_login_at: string | null
  created_at: string
  role_id: number
  role: RoleCode
  role_name: string
  branch_id: number | null
  branch_name: string
  /** all = every branch; branches = branch_ids; team = their team only; none = no panel access */
  scope: 'all' | 'branches' | 'team' | 'none'
  branch_ids: number[]
  self: boolean
}

export interface SystemUserInput {
  full_name?: string
  email?: string
  password?: string // create only
  phone?: string
  status?: 'ACTIVE' | 'INACTIVE'
  role?: RoleCode
  branch_id?: number // home branch for branch-level roles (0 = none)
  telegram_user_id?: number // 0 unlinks
}

/** NORMAL ends the same day; CROSS_DAY is a night shift ending the next day (22:00–06:00). */
export type WorkType = 'NORMAL' | 'CROSS_DAY'

/** A shift (work schedule): hours, grace period and usual work days. */
export interface WorkSchedule {
  id: number
  branch_id: number
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
  branch_id?: number // create: the branch it belongs to
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
  branch_id: number // its shifts are the ones to offer
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
  employee_id?: number
  employee_ids?: number[] // create: the same schedule for each of these people
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
  position_id: number | null
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
  kind?: 'STAFF' | 'SYSTEM'
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
  position?: string // by name; created if missing
  position_id?: number // from the list (0 clears)
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
  all_branches: boolean // false: limited to branch_ids (Branch assignment) or own branch
  branch_ids: number[]
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
  photo_id: number | null // the photo / location sent with this submission
  location_id: number | null
  reviewed_by: number | null
  reviewed_at: string | null
  reason: string
  created_at: string
  reviewer?: { id: number; employee_code: string; full_name: string }
}

/** The hours an employee was expected to work on a date (less a half-day leave). */
export interface PlannedDay {
  work: boolean
  start?: string
  end?: string
  grace_minutes: number
  cross_day: boolean
  start_next_day?: boolean
  shift_name?: string
  color?: string
  source: 'DEFAULT' | 'ROSTER' | 'SPECIAL'
  note?: string
}

export interface Attendance {
  id: number
  employee_id: number
  work_date: string
  check_in_at: string | null
  check_out_at: string | null
  check_in_reason: string // the employee's reason (required when late)
  check_out_reason: string // (required when leaving early)
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
  schedule: PlannedDay
  holiday: string
  leave: LeaveRequest | null // approved leave on the date (full or half day)
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

export type LeaveApproval = 'MANUAL' | 'AUTO'

/** A leave category with its yearly allowance and approval policy. */
export interface LeaveType {
  id: number
  branch_id: number
  code: string
  name: string
  annual_days: number // 0 = not counted
  is_paid: boolean
  approval: LeaveApproval
  allow_exceed: boolean
  max_days_per_request: number // 0 = no limit
  min_notice_days: number
  color: string
  description: string
  active: boolean
  sort_order: number
  requests?: number
}

export type LeaveTypeInput = Partial<Omit<LeaveType, 'id' | 'requests'>>

export interface LeaveBalance {
  leave_type_id: number
  code: string
  name: string
  color: string
  year: number
  tracked: boolean
  allocated: number
  carried: number
  used: number
  pending: number
  remaining: number
  available: number
  custom: boolean
  note?: string
}

export interface EmployeeBalances {
  employee_id: number
  employee_code: string
  full_name: string
  branch: string
  department: string
  can_edit: boolean
  balances: LeaveBalance[]
}

export interface BalancesResponse {
  year: number
  items: EmployeeBalances[]
  types: LeaveType[]
}

export type DayType = 'FULL' | 'FIRST_HALF' | 'SECOND_HALF'

export interface LeaveRequest {
  id: number
  employee_id: number
  start_date: string
  end_date: string
  day_type: DayType
  half_from: string | null // a half day's hours (from that day's shift)
  half_to: string | null
  days: number // 0.5 for a half day
  reason: string
  status: LeaveStatus
  reviewed_at: string | null
  review_note: string
  created_at: string
  employee?: Employee
  leave_type?: LeaveType
  reviewer?: Employee
}

/** Where someone checked in or out, and how far from the office (null: no office set). */
export interface Place {
  latitude: number
  longitude: number
  distance_meters: number | null
  inside_office: boolean | null
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
  check_in_reason?: string // why late (asked when late)
  check_out_reason?: string // why early (asked when leaving early)
  check_in_place?: Place // where they were (when shared)
  check_out_place?: Place
  working_minutes: number
  late_minutes: number
  early_leave_minutes: number
  status: DayStatus
  confirmation_status: ConfirmationStatus | ''
  confirmed_by: string
  leave_type: string
  leave_half?: 'FIRST_HALF' | 'SECOND_HALF' // half the day was leave
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

/** Company settings. Telegram and GPS belong to each branch (Branch). */
export interface CompanySettings {
  company_name: string
  timezone: string
  confirmation_required: boolean
  daily_report_enabled: boolean
  daily_report_time: string
  missing_checkin_alert_time: string
  day_close_time: string
}

export type SettingsUpdate = Partial<CompanySettings>

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
  branch_id: number
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
