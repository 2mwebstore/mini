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

  submit(type: AttendanceType, photo: Blob, location: Coordinates | null) {
    const form = new FormData()
    form.append('photo', photo, 'photo.jpg')
    if (location) {
      form.append('latitude', String(location.latitude))
      form.append('longitude', String(location.longitude))
      if (location.accuracy != null) form.append('accuracy', String(location.accuracy))
    }
    return api.post<SubmitResult>(type === 'CHECK_IN' ? '/attendance/check-in' : '/attendance/check-out', form)
  },

  photo: (photoId: number, signal?: AbortSignal) => api.get<Blob>(`/attendance/photos/${photoId}`, { responseType: 'blob', signal }),
}
