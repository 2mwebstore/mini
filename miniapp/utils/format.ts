import type { ConfirmationStatus, DayStatus, LeaveStatus } from '~/types/api'

// Clock times are shown in the company timezone (not the phone's), matching the
// bot, reports and the server's late/early calculation.
export function formatClock(iso: string | null | undefined, timeZone: string): string {
  if (!iso) return '—'
  return new Intl.DateTimeFormat('en-GB', { hour: '2-digit', minute: '2-digit', hour12: false, timeZone }).format(new Date(iso))
}

// Calendar dates arrive as midnight UTC representing the company-local day.
export function formatDate(iso: string, style: 'short' | 'long' = 'short'): string {
  const opts: Intl.DateTimeFormatOptions =
    style === 'long'
      ? { weekday: 'long', day: 'numeric', month: 'long', year: 'numeric', timeZone: 'UTC' }
      : { weekday: 'short', day: '2-digit', month: 'short', timeZone: 'UTC' }
  return new Intl.DateTimeFormat('en-GB', opts).format(new Date(iso))
}

export function formatDateRange(start: string, end: string): string {
  const s = new Date(start)
  const e = new Date(end)
  const day = (d: Date, year: boolean) =>
    new Intl.DateTimeFormat('en-GB', { day: '2-digit', month: 'short', year: year ? 'numeric' : undefined, timeZone: 'UTC' }).format(d)
  return s.getTime() === e.getTime() ? day(s, true) : `${day(s, false)} → ${day(e, true)}`
}

export function formatMinutes(m: number): string {
  if (!m) return '0m'
  const h = Math.floor(m / 60)
  const r = m % 60
  return h ? (r ? `${h}h ${r}m` : `${h}h`) : `${r}m`
}

/** YYYY-MM-DD of "now" in the company timezone. */
export function todayISO(timeZone: string): string {
  return new Intl.DateTimeFormat('en-CA', { year: 'numeric', month: '2-digit', day: '2-digit', timeZone }).format(new Date())
}

export function monthKey(iso: string): string {
  return iso.slice(0, 7)
}

export function shiftMonth(key: string, delta: number): string {
  const [y, m] = key.split('-').map(Number) as [number, number]
  const d = new Date(Date.UTC(y, m - 1 + delta, 1))
  return d.toISOString().slice(0, 7)
}

export function formatMonth(key: string): string {
  return new Intl.DateTimeFormat('en-GB', { month: 'long', year: 'numeric', timeZone: 'UTC' }).format(new Date(`${key}-01T00:00:00Z`))
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
  PENDING: { label: 'Pending review', tone: 'warning' },
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

/** YYYY-MM-DD of an instant in the company timezone. */
export function todayISOAt(iso: string, timeZone: string): string {
  return new Intl.DateTimeFormat('en-CA', { year: 'numeric', month: '2-digit', day: '2-digit', timeZone }).format(new Date(iso))
}
