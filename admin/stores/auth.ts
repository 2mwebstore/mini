import { defineStore } from 'pinia'
import { ApiError } from '~/services/api'
import { authService } from '~/services/auth.service'
import type { Employee, Permission, RoleCode } from '~/types/api'

// The signed-in admin. The API is the source of truth: permissions here only
// decide what the UI shows; every request is authorized again server-side.
export const useAuthStore = defineStore('auth', {
  state: () => ({
    checked: false,
    employee: null as Employee | null,
    role: null as RoleCode | null,
    permissions: [] as Permission[],
    allBranches: true,
    branchIds: [] as number[],
    company: { name: '', timezone: 'Asia/Phnom_Penh' },
  }),
  getters: {
    signedIn: s => !!s.employee,
    isReviewer: s => s.permissions.includes('team.review') || s.permissions.includes('all.review'),
  },
  actions: {
    can(p: Permission) {
      return this.permissions.includes(p)
    },
    /** Loads the session once; false when not signed in. */
    async ensure() {
      if (this.checked) return this.signedIn
      try {
        const me = await authService.me()
        this.employee = me.employee
        this.role = me.role
        this.permissions = me.permissions
        this.allBranches = me.all_branches
        this.branchIds = me.branch_ids
        this.company = me.company
      } catch (e) {
        if (!(e instanceof ApiError) || e.status !== 401) throw e
        this.clear()
      }
      this.checked = true
      return this.signedIn
    },
    async login(email: string, password: string) {
      await authService.login(email, password)
      this.checked = false
      await this.ensure()
    },
    async logout() {
      try {
        await authService.logout()
      } finally {
        this.clear()
        this.checked = true
      }
    },
    clear() {
      this.employee = null
      this.role = null
      this.permissions = []
      this.allBranches = true
      this.branchIds = []
    },
  },
})
