import { configureApi } from '~/services/api'
import { useAuthStore } from '~/stores/auth'

// Client-only start-up: tell Telegram the app is ready, size it to full height,
// match the header to the theme, then load who the user is.
export default defineNuxtPlugin(async () => {
  configureApi(useRuntimeConfig().public.apiBase)

  const tg = window.Telegram?.WebApp
  if (tg) {
    tg.ready()
    tg.expand()
    tg.disableVerticalSwipes?.() // keep swipe-down from closing the app mid-form
    if (tg.isVersionAtLeast('6.1')) {
      tg.setHeaderColor('secondary_bg_color')
      tg.setBackgroundColor('secondary_bg_color')
    }
    // Native controls (date pickers, scrollbars) follow Telegram's theme, not the phone's.
    const syncScheme = () => document.documentElement.style.setProperty('color-scheme', tg.colorScheme)
    syncScheme()
    tg.onEvent('themeChanged', syncScheme)
  }

  const auth = useAuthStore()
  if (tg?.initData) await auth.load()

  // Notifications in group chats link to t.me/<bot>?startapp=<page>; open that page.
  const page = tg?.initDataUnsafe.start_param
  if (page && startPages.includes(page) && auth.status === 'ready') {
    useRouter().replace(`/${page}`)
  }
})

const startPages = ['team', 'leave', 'history']
