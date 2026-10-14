import type { Items, LeaveBalance, LeaveInput, LeavePreview, LeaveRequest, LeaveType } from '~/types/api'
import { api } from './api'

export const leaveService = {
  types: () => api.get<Items<LeaveType>>('/leave/types').then(r => r.items ?? []),
  mine: () => api.get<Items<LeaveRequest>>('/leave/mine').then(r => r.items ?? []),
  balances: () => api.get<Items<LeaveBalance>>('/leave/balances').then(r => r.items ?? []),
  preview: (input: Omit<LeaveInput, 'reason'>) => api.post<LeavePreview>('/leave/preview', input),
  submit: (input: LeaveInput) => api.post<LeaveRequest>('/leave', input),
  cancel: (id: number) => api.post<LeaveRequest>(`/leave/${id}/cancel`),
}
