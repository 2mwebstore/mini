import type { BalancesResponse, Items, LeaveRequest, LeaveType, LeaveTypeInput } from '~/types/api'
import { api } from './api'

export const leaveService = {
  list: (query: { status?: string; employee_id?: number; branch_id?: number; from?: string; to?: string; day_type?: string; limit?: number }) =>
    api.get<Items<LeaveRequest>>('/leave', query).then(r => r.items ?? []),
  pending: () => api.get<Items<LeaveRequest>>('/leave/pending').then(r => r.items ?? []),
  types: () => api.get<Items<LeaveType>>('/leave/types').then(r => r.items ?? []),
  /** Every type, inactive too, with how many requests use it. */
  allTypes: () => api.get<Items<LeaveType>>('/leave/types', { all: 1 }).then(r => r.items ?? []),
  createType: (input: LeaveTypeInput) => api.post<LeaveType>('/leave/types', input),
  updateType: (id: number, input: LeaveTypeInput) => api.put<LeaveType>(`/leave/types/${id}`, input),
  deleteType: (id: number) => api.del(`/leave/types/${id}`),
  balances: (q: { year?: number; branch_id?: string; department_id?: string; q?: string }) => api.get<BalancesResponse>('/leave/balances', q),
  setBalance: (input: { employee_id: number; leave_type_id: number; year: number; allocated: number; carried: number; note: string }) =>
    api.put('/leave/balances', input),
  resetBalance: (employeeId: number, typeId: number, year: number) =>
    api.del(`/leave/balances?employee_id=${employeeId}&leave_type_id=${typeId}&year=${year}`),
  approve: (id: number, note = '') => api.post<LeaveRequest>(`/leave/${id}/approve`, { note }),
  reject: (id: number, note: string) => api.post<LeaveRequest>(`/leave/${id}/reject`, { note }),
}
