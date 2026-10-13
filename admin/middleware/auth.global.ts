import { useAuthStore } from '~/stores/auth'
import type { Permission } from '~/types/api'

// UX guard only: pages declare `definePageMeta({ permission })`; the API enforces
// the same permission on every request regardless of what the UI shows.
export default defineNuxtRouteMiddleware(async to => {
  const auth = useAuthStore()
  const signedIn = await auth.ensure().catch(() => false)
  if (to.meta.public) {
    return signedIn && to.path === '/login' ? navigateTo('/') : undefined
  }
  if (!signedIn) return navigateTo({ path: '/login', query: to.fullPath !== '/' ? { redirect: to.fullPath } : {} })
  const need = to.meta.permission as Permission | Permission[] | undefined
  if (need && !(Array.isArray(need) ? need.some(p => auth.can(p)) : auth.can(need))) return navigateTo('/')
})
