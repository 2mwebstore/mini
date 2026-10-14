import { useAuthStore } from '~/stores/auth'

// System users (admin accounts) have no attendance or leave: the app opens on the
// Team tab for them. UX only — the API refuses those endpoints anyway.
const staffPages = /^\/($|history|leave|check)/

export default defineNuxtRouteMiddleware(to => {
  const auth = useAuthStore()
  if (auth.isSystemUser && staffPages.test(to.path)) return navigateTo(auth.isReviewer ? '/team' : '/profile', { replace: true })
})
