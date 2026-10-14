import type { Items, OvertimeInput, OvertimePlan, OvertimeRequest } from '~/types/api'
import { api } from './api'

// The employee's overtime: the suggested hours for a date, their requests, ask, cancel.
export const overtimeService = {
  plan: (date: string) => api.get<OvertimePlan>('/overtime/plan', { query: { date } }),
  mine: () => api.get<Items<OvertimeRequest>>('/overtime/mine').then(r => r.items ?? []),
  submit: (input: OvertimeInput) => api.post<OvertimeRequest>('/overtime', input),
  cancel: (id: number) => api.post<OvertimeRequest>(`/overtime/${id}/cancel`),
}
