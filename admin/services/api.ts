// The one API client for the admin panel. The session is an HttpOnly cookie the
// browser sends automatically (same origin); X-Requested-With is required by the
// API on cookie-authenticated writes (CSRF defence).

export class ApiError extends Error {
  constructor(
    public status: number,
    public code: string,
    message: string,
  ) {
    super(message)
  }
}

let base = '/api/v1'
let onUnauthorized: (() => void) | undefined

export function configureApi(opts: { base: string; onUnauthorized: () => void }) {
  base = opts.base.replace(/\/$/, '')
  onUnauthorized = opts.onUnauthorized
}

/** Absolute URL for links the browser opens itself (photos, exports). */
export function apiUrl(path: string, query?: Query) {
  return base + path + queryString(query)
}

type Query = Record<string, string | number | boolean | undefined | null>

function queryString(query?: Query) {
  if (!query) return ''
  const q = new URLSearchParams()
  for (const [k, v] of Object.entries(query)) if (v !== undefined && v !== null && v !== '') q.set(k, String(v))
  const s = q.toString()
  return s ? `?${s}` : ''
}

const fallbackMessages: Record<number, string> = {
  0: 'Cannot reach the server. Check your connection and try again.',
  401: 'Your session has ended. Please sign in again.',
  403: 'You do not have permission to do this.',
  404: 'Not found.',
  429: 'Too many requests. Please wait a moment.',
  502: 'The server is temporarily unavailable. Please try again.',
  503: 'The server is temporarily unavailable. Please try again.',
}

async function request<T>(method: string, path: string, body?: unknown, query?: Query): Promise<T> {
  const headers: Record<string, string> = { 'X-Requested-With': 'fetch' }
  if (body !== undefined) headers['Content-Type'] = 'application/json'
  let res: Response
  try {
    res = await fetch(base + path + queryString(query), {
      method,
      headers,
      body: body === undefined ? undefined : JSON.stringify(body),
      credentials: 'same-origin',
    })
  } catch {
    throw new ApiError(0, 'NETWORK', fallbackMessages[0]!)
  }
  if (!res.ok) {
    const data = await res.json().catch(() => null)
    const err = data?.error
    if (res.status === 401 && !path.startsWith('/auth/')) onUnauthorized?.()
    throw new ApiError(
      res.status,
      err?.code ?? `HTTP_${res.status}`,
      err?.message ?? fallbackMessages[res.status] ?? 'Something went wrong. Please try again.',
    )
  }
  if (res.status === 204) return undefined as T
  return res.json() as Promise<T>
}

export const api = {
  get: <T>(path: string, query?: Query) => request<T>('GET', path, undefined, query),
  post: <T>(path: string, body?: unknown) => request<T>('POST', path, body ?? {}),
  put: <T>(path: string, body: unknown) => request<T>('PUT', path, body),
  del: <T = void>(path: string, body?: unknown) => request<T>('DELETE', path, body),
}
