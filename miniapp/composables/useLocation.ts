import type { Coordinates } from '~/services/attendance.service'

export type LocationState = 'idle' | 'locating' | 'ok' | 'denied' | 'unavailable'

// Current position for check-in/out: Telegram's LocationManager (Bot API 8.0+)
// when available, otherwise the browser Geolocation API.
export function useLocation() {
  const state = ref<LocationState>('idle')
  const coords = ref<Coordinates | null>(null)
  const canOpenSettings = ref(false)

  async function locate(): Promise<Coordinates | null> {
    state.value = 'locating'
    coords.value = null
    const found = (await fromTelegram()) ?? (await fromBrowser())
    if (found && found !== 'denied') {
      coords.value = found
      state.value = 'ok'
    } else {
      state.value = found === 'denied' ? 'denied' : 'unavailable'
    }
    return coords.value
  }

  function openSettings() {
    webApp()?.LocationManager?.openSettings()
  }

  // undefined = not supported here (try the browser), 'denied' = user refused.
  // Telegram answers through client events; if a client never does, give up
  // (init quickly, the permission prompt generously) instead of spinning forever.
  function fromTelegram(): Promise<Coordinates | 'denied' | undefined> {
    const lm = webApp()?.LocationManager
    if (!lm || !webApp()?.isVersionAtLeast('8.0')) return Promise.resolve(undefined)
    return new Promise(resolve => {
      let done = false
      const finish = (v: Coordinates | 'denied' | undefined) => {
        if (!done) {
          done = true
          clearTimeout(timer)
          resolve(v)
        }
      }
      let timer = setTimeout(() => finish(undefined), 5000)
      const get = () => {
        if (!lm.isLocationAvailable) return finish(undefined)
        clearTimeout(timer)
        timer = setTimeout(() => finish(undefined), 30000)
        lm.getLocation(data => {
          if (data) return finish({ latitude: data.latitude, longitude: data.longitude, accuracy: data.horizontal_accuracy })
          canOpenSettings.value = lm.isAccessRequested && !lm.isAccessGranted
          finish('denied')
        })
      }
      lm.isInited ? get() : lm.init(get)
    })
  }

  function fromBrowser(): Promise<Coordinates | 'denied' | undefined> {
    if (!('geolocation' in navigator)) return Promise.resolve(undefined)
    return new Promise(resolve => {
      navigator.geolocation.getCurrentPosition(
        p => resolve({ latitude: p.coords.latitude, longitude: p.coords.longitude, accuracy: p.coords.accuracy }),
        err => resolve(err.code === err.PERMISSION_DENIED ? 'denied' : undefined),
        { enableHighAccuracy: true, timeout: 15000, maximumAge: 0 },
      )
    })
  }

  return { state, coords, canOpenSettings, locate, openSettings }
}
