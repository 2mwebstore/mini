import type { Items, LeaveRequest, LeaveType } from '~/types/api'
import { api } from './api'

export const leaveService = {
  list: (query: { status?: string; employee_id?: number; branch_id?: number; from?: string; to?: string; limit?: number }) =>
    api.get<Items<LeaveRequest>>('/leave', query).then(r => r.items ?? []),
  pending: () => api.get<Items<LeaveRequest>>('/leave/pending').then(r => r.items ?? []),
  types: () => api.get<Items<LeaveType>>('/leave/types').then(r => r.items ?? []),
  approve: (id: number, note = '') => api.post<LeaveRequest>(`/leave/${id}/approve`, { note }),
  reject: (id: number, note: string) => api.post<LeaveRequest>(`/leave/${id}/reject`, { note }),
}
