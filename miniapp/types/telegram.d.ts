// The subset of telegram-web-app.js used by this app.
// https://core.telegram.org/bots/webapps#initializing-mini-apps

interface TelegramBottomButton {
  setText(text: string): TelegramBottomButton
  show(): TelegramBottomButton
  hide(): TelegramBottomButton
  enable(): TelegramBottomButton
  disable(): TelegramBottomButton
  showProgress(leaveActive?: boolean): TelegramBottomButton
  hideProgress(): TelegramBottomButton
  onClick(cb: () => void): TelegramBottomButton
  offClick(cb: () => void): TelegramBottomButton
}

interface TelegramBackButton {
  show(): TelegramBackButton
  hide(): TelegramBackButton
  onClick(cb: () => void): TelegramBackButton
  offClick(cb: () => void): TelegramBackButton
}

interface TelegramLocationData {
  latitude: number
  longitude: number
  horizontal_accuracy: number | null
}

interface TelegramLocationManager {
  isInited: boolean
  isLocationAvailable: boolean
  isAccessRequested: boolean
  isAccessGranted: boolean
  init(cb?: () => void): TelegramLocationManager
  getLocation(cb: (data: TelegramLocationData | null) => void): TelegramLocationManager
  openSettings(): TelegramLocationManager
}

interface TelegramWebApp {
  initData: string
  initDataUnsafe: {
    user?: { id: number; first_name: string; last_name?: string; username?: string }
    start_param?: string // from t.me/<bot>?startapp=<param> links
  }
  version: string
  platform: string
  colorScheme: 'light' | 'dark'
  isVersionAtLeast(version: string): boolean
  ready(): void
  expand(): void
  close(): void
  disableVerticalSwipes?(): void
  setHeaderColor(color: string): void
  setBackgroundColor(color: string): void
  showAlert(message: string, cb?: () => void): void
  showConfirm(message: string, cb?: (ok: boolean) => void): void
  onEvent(event: string, cb: () => void): void
  offEvent(event: string, cb: () => void): void
  MainButton: TelegramBottomButton
  BackButton: TelegramBackButton
  HapticFeedback: {
    impactOccurred(style: 'light' | 'medium' | 'heavy' | 'rigid' | 'soft'): void
    notificationOccurred(type: 'error' | 'success' | 'warning'): void
    selectionChanged(): void
  }
  LocationManager?: TelegramLocationManager
}

interface Window {
  Telegram?: { WebApp: TelegramWebApp }
}
