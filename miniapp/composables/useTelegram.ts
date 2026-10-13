// Client-only wrapper around window.Telegram.WebApp. Every helper degrades to a
// browser fallback so the app still renders (with an "open in Telegram" screen)
// when opened outside Telegram.

export function webApp(): TelegramWebApp | undefined {
  return import.meta.client ? window.Telegram?.WebApp : undefined
}

/** True when running inside a Telegram client that sent signed launch data. */
export function insideTelegram(): boolean {
  return !!webApp()?.initData
}

export function haptic(type: 'success' | 'error' | 'warning') {
  webApp()?.HapticFeedback?.notificationOccurred(type)
}

export function confirmDialog(message: string): Promise<boolean> {
  const tg = webApp()
  if (tg?.isVersionAtLeast('6.2')) return new Promise(resolve => tg.showConfirm(message, ok => resolve(!!ok)))
  return Promise.resolve(window.confirm(message))
}

export function alertDialog(message: string): Promise<void> {
  const tg = webApp()
  if (tg?.isVersionAtLeast('6.2')) return new Promise(resolve => tg.showAlert(message, () => resolve()))
  window.alert(message)
  return Promise.resolve()
}

/** Shows Telegram's native back button while the calling page is mounted. */
export function useBackButton(onBack: () => void) {
  onMounted(() => {
    const tg = webApp()
    if (!tg?.isVersionAtLeast('6.1')) return
    tg.BackButton.onClick(onBack).show()
  })
  onBeforeUnmount(() => {
    const tg = webApp()
    if (!tg?.isVersionAtLeast('6.1')) return
    tg.BackButton.offClick(onBack).hide()
  })
}

/**
 * Drives Telegram's native bottom button from reactive state. `native` is false
 * outside Telegram; pages then render their own button instead.
 */
export function useMainButton(opts: {
  text: MaybeRefOrGetter<string>
  visible: MaybeRefOrGetter<boolean>
  disabled?: MaybeRefOrGetter<boolean>
  loading?: MaybeRefOrGetter<boolean>
  onClick: () => void
}) {
  const native = ref(false)
  let stop: (() => void) | undefined
  const handler = () => {
    if (!toValue(opts.disabled) && !toValue(opts.loading)) opts.onClick()
  }
  onMounted(() => {
    const tg = webApp()
    if (!tg?.initData) return
    native.value = true
    tg.MainButton.onClick(handler)
    stop = watchEffect(() => {
      const b = tg.MainButton
      b.setText(toValue(opts.text))
      toValue(opts.disabled) ? b.disable() : b.enable()
      toValue(opts.loading) ? b.showProgress(false) : b.hideProgress()
      toValue(opts.visible) ? b.show() : b.hide()
    })
  })
  onBeforeUnmount(() => {
    stop?.()
    const tg = webApp()
    if (!native.value || !tg) return
    tg.MainButton.offClick(handler).hideProgress().hide()
  })
  return { native }
}
