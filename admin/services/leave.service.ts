import type { BalancesResponse, LeaveRequest, LeaveType, LeaveTypeInput } from '~/types/api'
import { api, listAll, listPage, type Paging } from './api'

type LeaveQuery = { status?: string; employee_id?: number; branch_id?: number; from?: string; to?: string; day_type?: string }

export const leaveService = {
  page: (query: LeaveQuery, p: Paging) => listPage<LeaveRequest>('/leave', query, p),
  pending: () => listAll<LeaveRequest>('/leave/pending'),
  types: () => listAll<LeaveType>('/leave/types'),
  /** Every type, inactive too, with how many requests use it. */
  allTypes: (branchId?: number) => listAll<LeaveType>('/leave/types', { all: 1, branch_id: branchId || undefined }),
  typesPage: (branchId: number | undefined, p: Paging) => listPage<LeaveType>('/leave/types', { all: 1, branch_id: branchId || undefined }, p),
  createType: (input: LeaveTypeInput) => api.post<LeaveType>('/leave/types', input),
  updateType: (id: number, input: LeaveTypeInput) => api.put<LeaveType>(`/leave/types/${id}`, input),
  deleteType: (id: number) => api.del(`/leave/types/${id}`),
  balances: (q: { year?: number; branch_id?: string; department_id?: string; q?: string }) => api.get<BalancesResponse>('/leave/balances', { ...q, page_size: 0 }),
  setBalance: (input: { employee_id: number; leave_type_id: number; year: number; allocated: number; carried: number; note: string }) =>
    api.put('/leave/balances', input),
  resetBalance: (employeeId: number, typeId: number, year: number) =>
    api.del(`/leave/balances?employee_id=${employeeId}&leave_type_id=${typeId}&year=${year}`),
  approve: (id: number, note = '') => api.post<LeaveRequest>(`/leave/${id}/approve`, { note }),
  reject: (id: number, note: string) => api.post<LeaveRequest>(`/leave/${id}/reject`, { note }),
}
