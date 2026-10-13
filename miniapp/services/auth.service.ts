import type { MeResponse } from '~/types/api'
import { api } from './api'

export const authService = {
  me: () => api.get<MeResponse>('/me'),
}
