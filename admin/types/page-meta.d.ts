import type { Permission } from './api'

declare module '#app' {
  interface PageMeta {
    /** Reachable without signing in (login page). */
    public?: boolean
    /** Any of these permissions is required to open the page (UX only; the API enforces). */
    permission?: Permission | Permission[]
  }
}

export {}
