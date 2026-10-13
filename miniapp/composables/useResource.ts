import { ApiError } from '~/services/api'

// Loading / data / error / retry for one API-driven view. (Named useResource to
// avoid clashing with Nuxt's SSR-oriented useAsyncData.)
export function useResource<T>(fetcher: () => Promise<T>, opts: { immediate?: boolean } = {}) {
  const data = shallowRef<T | null>(null)
  const error = ref<ApiError | null>(null)
  const loading = ref(false)

  async function load() {
    loading.value = true
    error.value = null
    try {
      data.value = await fetcher()
    } catch (e) {
      error.value = e instanceof ApiError ? e : new ApiError(0, 'UNKNOWN', 'Something went wrong. Please try again.')
    } finally {
      loading.value = false
    }
  }

  if (opts.immediate !== false) onMounted(load)
  return { data, error, loading, load }
}
