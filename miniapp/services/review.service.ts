import type { DailyReport, Items, LeaveRequest, OvertimeRequest, PendingAttendance } from '~/types/api'
import { api } from './api'

// Reviewer actions. The API checks reviewer role and team scope on every call.
export const reviewService = {
  pendingAttendance: () => api.get<Items<PendingAttendance>>('/attendance/pending').then(r => r.items ?? []),
  pendingLeave: () => api.get<Items<LeaveRequest>>('/leave/pending').then(r => r.items ?? []),

  confirmAttendance: (confirmationId: number) => api.post(`/attendance/confirmations/${confirmationId}/confirm`),
  rejectAttendance: (confirmationId: number, reason: string) =>
    api.post(`/attendance/confirmations/${confirmationId}/reject`, { reason }),

  approveLeave: (id: number) => api.post(`/leave/${id}/approve`),
  rejectLeave: (id: number, note: string) => api.post(`/leave/${id}/reject`, { note }),

  pendingOvertime: () => api.get<Items<OvertimeRequest>>('/overtime/pending').then(r => r.items ?? []),
  approveOvertime: (id: number) => api.post(`/overtime/${id}/approve`),
  rejectOvertime: (id: number, note: string) => api.post(`/overtime/${id}/reject`, { note }),

  daily: (date?: string) => api.get<DailyReport>('/reports/daily', { query: { date } }),
}
