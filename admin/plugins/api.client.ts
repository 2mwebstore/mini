import { configureApi } from '~/services/api'
import { useAuthStore } from '~/stores/auth'

// An expired or revoked session anywhere sends the user back to sign in.
export default defineNuxtPlugin(() => {
  const router = useRouter()
  configureApi({
    base: useRuntimeConfig().public.apiBase,
    onUnauthorized: () => {
      useAuthStore().clear()
      const route = router.currentRoute.value
      if (route.path !== '/login') router.push({ path: '/login', query: { redirect: route.fullPath } })
    },
  })
})
