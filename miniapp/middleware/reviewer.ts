import { useAuthStore } from '~/stores/auth'

// UX guard only: the API independently refuses reviewer endpoints to staff.
export default defineNuxtRouteMiddleware(() => {
  if (!useAuthStore().isReviewer) return navigateTo('/')
})
