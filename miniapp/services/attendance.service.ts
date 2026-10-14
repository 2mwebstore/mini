import type { AttendanceType, HistoryResponse, SubmitResult, TodayStatus } from '~/types/api'
import { api } from './api'

export interface Coordinates {
  latitude: number
  longitude: number
  accuracy?: number | null
}

export const attendanceService = {
  today: () => api.get<TodayStatus>('/attendance/today'),

  history: (month: string) => api.get<HistoryResponse>('/attendance/history', { query: { month } }),

  // No photo: the location (when shared) is all a check-in/out sends.
  submit(type: AttendanceType, location: Coordinates | null) {
    const form = new FormData()
    if (location) {
      form.append('latitude', String(location.latitude))
      form.append('longitude', String(location.longitude))
      if (location.accuracy != null) form.append('accuracy', String(location.accuracy))
    }
    return api.post<SubmitResult>(type === 'CHECK_IN' ? '/attendance/check-in' : '/attendance/check-out', form)
  },

  photo: (photoId: number, signal?: AbortSignal) => api.get<Blob>(`/attendance/photos/${photoId}`, { responseType: 'blob', signal }),
}
