import type {
  Items, OverrideInput, Roster, RosterItem, RosterQuery, ScheduleOverride, ShiftInput, WorkSchedule,
} from '~/types/api'
import { api, apiUrl } from './api'

export const scheduleService = {
  // shifts
  shifts: (branchId?: number) => api.get<Items<WorkSchedule>>('/work-schedules', { branch_id: branchId || undefined }).then(r => r.items ?? []),
  createShift: (input: ShiftInput) => api.post<WorkSchedule>('/work-schedules', input),
  updateShift: (id: number, input: ShiftInput) => api.put<WorkSchedule>(`/work-schedules/${id}`, input),
  deleteShift: (id: number) => api.del(`/work-schedules/${id}`),

  // weekly roster
  roster: (q: RosterQuery) => api.get<Roster>('/roster', { ...q }),
  setRoster: (items: RosterItem[]) => api.put<{ updated: number }>('/roster', { items }),
  copyRoster: (from: string, to: string, branchId?: string) =>
    api.post<{ copied: number }>('/roster/copy', { from, to, branch_id: Number(branchId) || 0 }),
  exportUrl: (q: RosterQuery, format: 'xlsx' | 'csv') => apiUrl('/roster/export', { ...q, format }),

  // special / temporary schedules
  overrides: (q: { when?: string; branch_id?: string; q?: string }) =>
    api.get<Items<ScheduleOverride>>('/schedule-overrides', q).then(r => r.items ?? []),
  createOverride: (input: OverrideInput) => api.post<Items<ScheduleOverride>>('/schedule-overrides', input).then(r => r.items ?? []),
  updateOverride: (id: number, input: OverrideInput) => api.put<ScheduleOverride>(`/schedule-overrides/${id}`, input),
  deleteOverride: (id: number) => api.del(`/schedule-overrides/${id}`),
}
