import type { ConfirmationStatus, DayStatus, EmployeeStatus, LeaveStatus, RoleCode } from '~/types/api'

// Clock times are shown in the company timezone, matching the server's
// late/early calculation and the Mini App.
export function formatClock(iso: string | null | undefined, timeZone: string): string {
  if (!iso) return '—'
  return new Intl.DateTimeFormat('en-GB', { hour: '2-digit', minute: '2-digit', hour12: false, timeZone }).format(new Date(iso))
}

export function formatDateTime(iso: string | null | undefined, timeZone: string): string {
  if (!iso) return '—'
  return new Intl.DateTimeFormat('en-GB', { day: '2-digit', month: 'short', year: 'numeric', hour: '2-digit', minute: '2-digit', hour12: false, timeZone }).format(new Date(iso))
}

// Calendar dates arrive as midnight UTC representing the company-local day.
export function formatDate(iso: string | null | undefined, withWeekday = false): string {
  if (!iso) return '—'
  return new Intl.DateTimeFormat('en-GB', { weekday: withWeekday ? 'short' : undefined, day: '2-digit', month: 'short', year: 'numeric', timeZone: 'UTC' }).format(new Date(iso))
}

export function formatDateRange(start: string, end: string): string {
  return start === end ? formatDate(start) : `${formatDate(start)} → ${formatDate(end)}`
}

export function formatMinutes(m: number | null | undefined): string {
  if (!m) return '—'
  const h = Math.floor(m / 60)
  const r = m % 60
  return h ? (r ? `${h}h ${r}m` : `${h}h`) : `${r}m`
}

/** YYYY-MM-DD of "now" in the company timezone. */
export function todayISO(timeZone: string): string {
  return new Intl.DateTimeFormat('en-CA', { year: 'numeric', month: '2-digit', day: '2-digit', timeZone }).format(new Date())
}

export function monthStartISO(timeZone: string): string {
  return `${todayISO(timeZone).slice(0, 8)}01`
}

/** YYYY-MM-DD from an API calendar date. */
export function isoDay(iso: string | null | undefined): string {
  return iso ? iso.slice(0, 10) : ''
}

export type Tone = 'success' | 'warning' | 'danger' | 'info' | 'neutral'

export const dayStatus: Record<DayStatus, { label: string; tone: Tone }> = {
  PRESENT: { label: 'Present', tone: 'success' },
  LATE: { label: 'Late', tone: 'warning' },
  EARLY_LEAVE: { label: 'Early leave', tone: 'warning' },
  ABSENT: { label: 'Absent', tone: 'danger' },
  LEAVE: { label: 'On leave', tone: 'info' },
  HOLIDAY: { label: 'Holiday', tone: 'neutral' },
  WEEKEND: { label: 'Weekend', tone: 'neutral' },
  NOT_CHECKED_IN: { label: 'Not checked in', tone: 'neutral' },
}

export const confirmationStatus: Record<ConfirmationStatus, { label: string; tone: Tone }> = {
  PENDING: { label: 'Pending', tone: 'warning' },
  CONFIRMED: { label: 'Confirmed', tone: 'success' },
  REJECTED: { label: 'Rejected', tone: 'danger' },
  SUPERSEDED: { label: 'Re-submitted', tone: 'neutral' },
}

export const leaveStatus: Record<LeaveStatus, { label: string; tone: Tone }> = {
  PENDING: { label: 'Pending', tone: 'warning' },
  APPROVED: { label: 'Approved', tone: 'success' },
  REJECTED: { label: 'Rejected', tone: 'danger' },
  CANCELLED: { label: 'Cancelled', tone: 'neutral' },
}

export const employeeStatus: Record<EmployeeStatus, { label: string; tone: Tone }> = {
  ACTIVE: { label: 'Active', tone: 'success' },
  SUSPENDED: { label: 'Suspended', tone: 'warning' },
  INACTIVE: { label: 'Inactive', tone: 'neutral' },
}

export const roleLabels: Record<RoleCode, string> = {
  super_admin: 'Owner (Super Admin)',
  hr_admin: 'HR / Admin',
  branch_admin: 'Branch Admin',
  manager: 'Manager',
  staff: 'Staff',
}

export const typeLabel = (t: string) => (t === 'CHECK_IN' ? 'Check-in' : 'Check-out')

// ---------- shifts ----------

export const weekdayShort = ['Mon', 'Tue', 'Wed', 'Thu', 'Fri', 'Sat', 'Sun'] as const

/** "22:00–06:00 (+1)" — the (+1) marks a cross-day (night) shift ending the next day. */
export function shiftHours(start?: string | null, end?: string | null, crossDay?: boolean): string {
  if (!start || !end) return ''
  return `${start}–${end}${crossDay ? ' (+1)' : ''}`
}

/** "1,2,3,4,5" -> [1,2,3,4,5] (ISO weekdays, 1 = Monday). */
export function parseWorkDays(csv: string | null | undefined): number[] {
  return (csv ?? '').split(',').map(Number).filter(d => d >= 1 && d <= 7)
}

/** [1,2,3,4,5] -> "Mon–Fri"; other sets as a list ("Mon, Wed, Sat"). */
export function workDaysLabel(days: number[]): string {
  const s = [...new Set(days)].sort((a, b) => a - b)
  if (s.length === 7) return 'Every day'
  if (s.length > 2 && s.every((d, i) => i === 0 || d === s[i - 1]! + 1)) return `${weekdayShort[s[0]! - 1]}–${weekdayShort[s[s.length - 1]! - 1]}`
  return s.map(d => weekdayShort[d - 1]).join(', ')
}

/** ISO weekday of a YYYY-MM-DD (1 = Monday). */
export function isoWeekday(day: string): number {
  return ((new Date(`${day.slice(0, 10)}T00:00:00Z`).getUTCDay() + 6) % 7) + 1
}

/** YYYY-MM-DD plus n days. */
export function addDays(day: string, n: number): string {
  const d = new Date(`${day.slice(0, 10)}T00:00:00Z`)
  d.setUTCDate(d.getUTCDate() + n)
  return d.toISOString().slice(0, 10)
}

/** Monday of the week containing day. */
export function mondayOf(day: string): string {
  return addDays(day, 1 - isoWeekday(day))
}

/** True when a check-out happened on a later calendar day than the work date (night shift). */
export function nextDay(workDate: string, at: string | null | undefined, timeZone: string): boolean {
  if (!at) return false
  return todayISOAt(at, timeZone) > workDate.slice(0, 10)
}

function todayISOAt(iso: string, timeZone: string): string {
  return new Intl.DateTimeFormat('en-CA', { year: 'numeric', month: '2-digit', day: '2-digit', timeZone }).format(new Date(iso))
}

/** "20 Oct" (calendar dates, no year). */
export function formatDayMonth(iso: string | null | undefined): string {
  if (!iso) return '—'
  return new Intl.DateTimeFormat('en-GB', { day: '2-digit', month: 'short', timeZone: 'UTC' }).format(new Date(iso))
}

/** "3 days", "2.5 days". */
export function fmtDays(n: number): string {
  return `${n % 1 ? n.toFixed(1) : n} day${n === 1 ? '' : 's'}`
}

/** "Half day · first half 09:00–13:15", or "3 days" for a full-day request. */
export function leaveSpan(l: { day_type?: string; half_from?: string | null; half_to?: string | null; days: number }): string {
  if (!l.day_type || l.day_type === 'FULL') return fmtDays(l.days)
  const part = l.day_type === 'FIRST_HALF' ? 'first half' : 'second half'
  return `Half day · ${part}${l.half_from && l.half_to ? ` ${l.half_from}–${l.half_to}` : ''}`
}

/** The latest review of a check-in or check-out (re-submissions supersede earlier ones). */
export function latestReview<T extends { id: number; type: string; status: string }>(confs: T[] | undefined, type: 'CHECK_IN' | 'CHECK_OUT'): T | undefined {
  return (confs ?? []).filter(c => c.type === type && c.status !== 'SUPERSEDED').sort((a, b) => b.id - a.id)[0]
}
