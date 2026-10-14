import type { Branch, BranchInput } from '~/types/api'
import { api, listAll, listPage, type Paging } from './api'

export const branchService = {
  list: () => listAll<Branch>('/branches'),
  page: (p: Paging) => listPage<Branch>('/branches', {}, p),
  get: (id: number) => api.get<Branch>(`/branches/${id}`),
  create: (input: BranchInput) => api.post<Branch>('/branches', input),
  update: (id: number, input: BranchInput) => api.put<Branch>(`/branches/${id}`, input),
  /** The owner, or an admin who manages the branch's accounts. null removes the branch's admin. */
  assignAdmin: (id: number, employeeId: number | null) => api.put<Branch>(`/branches/${id}/admin`, { employee_id: employeeId }),
}
