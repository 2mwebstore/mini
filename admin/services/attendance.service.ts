import type { Attendance, AttendanceDetail, AttendanceFilter, Items, ManualAttendanceInput, Page, PendingAttendance } from '~/types/api'
import { api, apiUrl } from './api'

export const attendanceService = {
  list: (filter: AttendanceFilter) => api.get<Page<Attendance>>('/attendance', { ...filter }),
  get: (id: number) => api.get<AttendanceDetail>(`/attendance/${id}`),
  pending: () => api.get<Items<PendingAttendance>>('/attendance/pending').then(r => r.items ?? []),
  confirm: (confirmationId: number) => api.post(`/attendance/confirmations/${confirmationId}/confirm`),
  reject: (confirmationId: number, reason: string) => api.post(`/attendance/confirmations/${confirmationId}/reject`, { reason }),
  create: (input: ManualAttendanceInput) => api.post<Attendance>('/attendance', input),
  edit: (id: number, input: Omit<ManualAttendanceInput, 'employee_id' | 'date'>) => api.put<Attendance>(`/attendance/${id}`, input),
  /** Deletes the day with its reviews, locations and photos; the reason is audited. */
  remove: (id: number, reason: string) => api.del(`/attendance/${id}`, { reason }),
  photoUrl: (photoId: number) => apiUrl(`/attendance/photos/${photoId}`),
}
