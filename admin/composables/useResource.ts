import { ApiError } from '~/services/api'

// Loading / data / error / retry for one API-driven view. (Named useResource to
// avoid clashing with Nuxt's SSR-oriented useAsyncData.)
export function useResource<T>(fetcher: () => Promise<T>, opts: { immediate?: boolean } = {}) {
  const data = shallowRef<T | null>(null)
  const error = ref<ApiError | null>(null)
  const loading = ref(false)
  let seq = 0

  async function load() {
    const mine = ++seq
    loading.value = true
    error.value = null
    try {
      const v = await fetcher()
      if (mine === seq) data.value = v
    } catch (e) {
      if (mine === seq) error.value = e instanceof ApiError ? e : new ApiError(0, 'UNKNOWN', 'Something went wrong. Please try again.')
    } finally {
      if (mine === seq) loading.value = false
    }
  }

  if (opts.immediate !== false) onMounted(load)
  return { data, error, loading, load }
}
