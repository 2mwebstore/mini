import type { Paged, Paging } from '~/services/api'

/** Rows-per-page choices; every list starts at the first. */
export const pageSizes = [10, 25, 50, 100] as const

/**
 * A list the API pages (10 rows by default): `load()` fetches the current page,
 * `go(page)` / `resize(size)` move around, and `reset()` goes back to page 1 —
 * call it when a filter changes.
 */
export function usePaged<T>(fetcher: (p: Paging) => Promise<Paged<T>>, opts: { immediate?: boolean; size?: number } = {}) {
  const page = ref(1)
  const pageSize = ref<number>(opts.size ?? pageSizes[0])
  const res = useResource(() => fetcher({ page: page.value, page_size: pageSize.value }), { immediate: opts.immediate })
  const rows = computed(() => res.data.value?.items ?? [])
  const total = computed(() => res.data.value?.total ?? 0)
  function go(p: number) {
    page.value = p
    res.load()
  }
  function resize(size: number) {
    pageSize.value = size
    go(1)
  }
  /** Back to the first page and reload (after a filter change). */
  const reset = () => go(1)
  /** Reload, stepping back a page when the last row of this one went away. */
  async function refresh() {
    await res.load()
    if (!rows.value.length && page.value > 1) go(page.value - 1)
  }
  return { ...res, rows, total, page, pageSize, go, resize, reset, refresh }
}

/** The same paging over rows already loaded (lists with totals, groups or a grid). */
export function usePager<T>(all: MaybeRefOrGetter<T[]>, size: number = pageSizes[0]) {
  const page = ref(1)
  const pageSize = ref<number>(size)
  const total = computed(() => toValue(all).length)
  // Stay on a page that exists when the rows change (a filter, a delete).
  watch(total, (n) => {
    const last = Math.max(1, Math.ceil(n / pageSize.value))
    if (page.value > last) page.value = last
  })
  const rows = computed(() => toValue(all).slice((page.value - 1) * pageSize.value, page.value * pageSize.value))
  const go = (p: number) => (page.value = p)
  const resize = (n: number) => {
    pageSize.value = n
    page.value = 1
  }
  const reset = () => (page.value = 1)
  return { rows, total, page, pageSize, go, resize, reset }
}
