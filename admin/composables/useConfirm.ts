// One confirmation dialog for the whole panel (ConfirmDialog in app.vue), asked for
// from anywhere: `const r = await confirm({ … }); if (!r) return`. With `reason`,
// the dialog also asks why (for audited changes) and returns it.

export interface ConfirmOptions {
  title: string // "Delete this record?"
  message?: string // what happens, in a sentence or two
  details?: { label: string; value: string }[] // what is being removed
  confirmLabel?: string // default "Delete"
  tone?: 'danger' | 'primary' // default danger
  reason?: { label?: string; required?: boolean; placeholder?: string; presets?: string[] }
}

export type ConfirmResult = false | { reason: string }

interface ConfirmState {
  open: boolean
  options: ConfirmOptions | null
  resolve: ((r: ConfirmResult) => void) | null
}

const state = reactive<ConfirmState>({ open: false, options: null, resolve: null })

export function useConfirm() {
  function confirm(options: ConfirmOptions): Promise<ConfirmResult> {
    state.resolve?.(false) // a newer question replaces an unanswered one
    return new Promise(resolve => {
      state.options = options
      state.resolve = resolve
      state.open = true
    })
  }
  return { confirm }
}

/** For ConfirmDialog only. */
export function useConfirmState() {
  function answer(r: ConfirmResult) {
    const resolve = state.resolve
    state.resolve = null
    state.open = false
    resolve?.(r)
  }
  return { state, answer }
}
