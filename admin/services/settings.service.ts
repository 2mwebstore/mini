import type { Holiday, Items, SettingsResponse, SettingsUpdate } from '~/types/api'
import { api } from './api'

export const settingsService = {
  get: () => api.get<SettingsResponse>('/settings'),
  update: (changes: SettingsUpdate) => api.put<SettingsResponse>('/settings', changes),
  // Holidays belong to a branch; one can be added to several branches at once.
  holidays: (year: number, branchId?: number) => api.get<Items<Holiday>>('/holidays', { year, branch_id: branchId || undefined }).then(r => r.items ?? []),
  addHoliday: (date: string, name: string, branchIds: number[]) => api.post<Items<Holiday>>('/holidays', { date, name, branch_ids: branchIds }),
  deleteHoliday: (id: number) => api.del(`/holidays/${id}`),
}
