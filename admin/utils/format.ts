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
