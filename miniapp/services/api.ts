// The one API client. Every request carries the signed Telegram initData
// ("Authorization: tma …"); the Go API validates it and applies all permissions.

export class ApiError extends Error {
  constructor(
    public status: number,
    public code: string,
    message: string,
  ) {
    super(message)
  }
}

let base = '/api/v1/app'

export function configureApi(apiBase: string) {
  base = apiBase.replace(/\/$/, '')
}

function initData(): string {
  return window.Telegram?.WebApp?.initData ?? ''
}

const fallbackMessages: Record<number, string> = {
  0: 'No connection. Check your internet and try again.',
  401: 'Please open this app from the Telegram bot.',
  403: 'You do not have permission to do this.',
  404: 'Not found.',
  413: 'The photo is too large. Please take it again.',
  429: 'Too many requests. Please wait a moment.',
  502: 'The server is temporarily unavailable. Please try again.',
  503: 'The server is temporarily unavailable. Please try again.',
}

interface RequestOptions {
  body?: unknown
  query?: Record<string, string | number | undefined>
  responseType?: 'json' | 'blob'
  signal?: AbortSignal
}

async function request<T>(method: string, path: string, opts: RequestOptions = {}): Promise<T> {
  const headers: Record<string, string> = { Authorization: `tma ${initData()}` }
  let body: BodyInit | undefined
  if (opts.body instanceof FormData) {
    body = opts.body
  } else if (opts.body !== undefined) {
    headers['Content-Type'] = 'application/json'
    body = JSON.stringify(opts.body)
  }
  let url = base + path
  if (opts.query) {
    const q = new URLSearchParams()
    for (const [k, v] of Object.entries(opts.query)) if (v !== undefined && v !== '') q.set(k, String(v))
    if ([...q].length) url += `?${q}`
  }

  let res: Response
  try {
    res = await fetch(url, { method, headers, body, signal: opts.signal })
  } catch (e) {
    if ((e as Error).name === 'AbortError') throw e
    throw new ApiError(0, 'NETWORK', fallbackMessages[0]!)
  }
  if (!res.ok) {
    const data = await res.json().catch(() => null)
    const err = data?.error
    throw new ApiError(
      res.status,
      err?.code ?? `HTTP_${res.status}`,
      err?.message ?? fallbackMessages[res.status] ?? 'Something went wrong. Please try again.',
    )
  }
  if (res.status === 204) return undefined as T
  return (opts.responseType === 'blob' ? res.blob() : res.json()) as Promise<T>
}

export const api = {
  get: <T>(path: string, opts?: Omit<RequestOptions, 'body'>) => request<T>('GET', path, opts),
  post: <T>(path: string, body?: unknown) => request<T>('POST', path, { body }),
}
