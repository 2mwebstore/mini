import { defineStore } from 'pinia'
import { ApiError } from '~/services/api'
import { authService } from '~/services/auth.service'
import type { CompanySettings, Me, Profile } from '~/types/api'

// Who is using the app. Loaded once at start-up from the API (the source of truth);
// UI permission checks here only hide what the API would refuse anyway.
export const useAuthStore = defineStore('auth', {
  state: () => ({
    status: 'idle' as 'idle' | 'loading' | 'ready' | 'error',
    me: null as Me | null,
    profile: null as Profile | null,
    settings: null as CompanySettings | null,
    error: null as ApiError | null,
  }),
  getters: {
    isReviewer: s => !!s.me?.is_reviewer,
    timezone: s => s.settings?.timezone || 'Asia/Phnom_Penh',
  },
  actions: {
    async load() {
      this.status = 'loading'
      this.error = null
      try {
        const res = await authService.me()
        this.me = res.me
        this.profile = res.profile
        this.settings = res.settings
        this.status = 'ready'
      } catch (e) {
        this.error = e instanceof ApiError ? e : new ApiError(0, 'UNKNOWN', 'Something went wrong. Please try again.')
        this.status = 'error'
      }
    },
  },
})
