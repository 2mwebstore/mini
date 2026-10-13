import { ApiError } from '~/services/api'
import { useToastStore } from '~/stores/toast'

// Runs a mutation with a busy flag and reports the outcome as a toast.
// Resolves true on success; on failure the error is shown and kept in `error`.
export function useAction() {
  const busy = ref<string | null>(null)
  const error = ref<ApiError | null>(null)
  const toast = useToastStore()

  async function run(key: string, fn: () => Promise<unknown>, success?: string): Promise<boolean> {
    busy.value = key
    error.value = null
    try {
      await fn()
      if (success) toast.success(success)
      return true
    } catch (e) {
      error.value = e instanceof ApiError ? e : new ApiError(0, 'UNKNOWN', String(e))
      toast.error(e)
      return false
    } finally {
      busy.value = null
    }
  }

  return { busy, error, run }
}
