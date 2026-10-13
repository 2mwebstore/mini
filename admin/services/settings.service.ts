import type { Holiday, Items, SettingsResponse, SettingsUpdate } from '~/types/api'
import { api } from './api'

export const settingsService = {
  get: () => api.get<SettingsResponse>('/settings'),
  /** A new bot token is verified with Telegram before it is saved. */
  update: (changes: SettingsUpdate) => api.put<SettingsResponse>('/settings', changes),
  holidays: (year: number) => api.get<Items<Holiday>>('/holidays', { year }).then(r => r.items ?? []),
  addHoliday: (date: string, name: string) => api.post<Holiday>('/holidays', { date, name }),
  deleteHoliday: (id: number) => api.del(`/holidays/${id}`),
}
