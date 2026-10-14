import type { OvertimeRequest } from '~/types/api'
import { api, listAll, listPage, type Paging } from './api'

type OvertimeQuery = { status?: string; employee_id?: number; branch_id?: number; from?: string; to?: string }

// Overtime requests: employees ask in the Mini App; reviewers approve or reject.
export const overtimeService = {
  page: (query: OvertimeQuery, p: Paging) => listPage<OvertimeRequest>('/overtime', query, p),
  pending: () => listAll<OvertimeRequest>('/overtime/pending'),
  approve: (id: number, note = '') => api.post<OvertimeRequest>(`/overtime/${id}/approve`, { note }),
  reject: (id: number, note: string) => api.post<OvertimeRequest>(`/overtime/${id}/reject`, { note }),
}
