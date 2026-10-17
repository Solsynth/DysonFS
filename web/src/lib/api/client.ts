export type ErrorFields = Record<string, string[]>

interface ApiErrorBody {
  code?: string
  message?: string
  error?: string
  detail?: string
  errors?: ErrorFields
}

/** A typed failure from the DysonFS API. */
export class DriveError extends Error {
  readonly status: number
  readonly code: string
  readonly detail?: string
  readonly fields?: ErrorFields

  constructor(status: number, message: string, code?: string, detail?: string, fields?: ErrorFields) {
    super(message)
    this.name = 'DriveError'
    this.status = status
    this.code = code ?? `HTTP_${status}`
    this.detail = detail
    this.fields = fields
  }

  get isUnauthenticated(): boolean {
    return this.status === 401
  }

  get isForbidden(): boolean {
    return this.status === 403
  }

  get isNotFound(): boolean {
    return this.status === 404
  }
}

type Method = 'GET' | 'POST' | 'PUT' | 'PATCH' | 'DELETE'

export type QueryValue = string | number | boolean | null | undefined

export interface RequestOptions {
  method?: Method
  body?: unknown
  query?: Record<string, QueryValue>
  signal?: AbortSignal
  headers?: Record<string, string>
}

export interface Page<T> {
  items: T[]
  total: number
}

let unauthorizedHandler: (() => void) | null = null

/** Installed by the session store so every 401 lands in one place. */
export function onUnauthorized(handler: () => void) {
  unauthorizedHandler = handler
}

/** Lets request paths outside `request()` (XHR uploads) raise the same signal. */
export function raiseUnauthorized() {
  unauthorizedHandler?.()
}

export function buildUrl(path: string, query?: Record<string, QueryValue>): string {
  const params = new URLSearchParams()
  for (const [key, value] of Object.entries(query ?? {})) {
    if (value === undefined || value === null || value === '') continue
    params.set(key, String(value))
  }
  const suffix = params.size ? `?${params.toString()}` : ''
  return `/api${path}${suffix}`
}

async function readJson<T>(response: Response): Promise<T> {
  if (response.status === 204) return undefined as T
  const text = await response.text()
  if (!text) return undefined as T
  return JSON.parse(text) as T
}

async function parseError(response: Response): Promise<DriveError> {
  const fallback = new DriveError(
    response.status,
    response.statusText || 'The drive did not answer.',
  )
  try {
    const payload = (await response.json()) as ApiErrorBody
    const message =
      payload.message ||
      (typeof payload.error === 'string' ? payload.error : '') ||
      fallback.message
    return new DriveError(
      response.status,
      message,
      payload.code,
      payload.detail,
      payload.errors,
    )
  } catch {
    return fallback
  }
}

async function send(url: string, init: RequestInit): Promise<Response> {
  let response: Response
  try {
    response = await fetch(url, init)
  } catch (caught) {
    if (caught instanceof DOMException && caught.name === 'AbortError') throw caught
    throw new DriveError(0, 'The drive service is unreachable. Check the connection and retry.')
  }
  if (response.ok) return response

  const error = await parseError(response)
  if (error.status === 401) unauthorizedHandler?.()
  throw error
}

/** Sends one request to DysonFS. The session travels in an HttpOnly cookie. */
export async function request<T>(path: string, options: RequestOptions = {}): Promise<T> {
  const headers: Record<string, string> = {
    Accept: 'application/json',
    ...options.headers,
  }
  if (options.body !== undefined) headers['Content-Type'] = 'application/json'

  const response = await send(buildUrl(path, options.query), {
    method: options.method ?? 'GET',
    headers,
    credentials: 'same-origin',
    body: options.body === undefined ? undefined : JSON.stringify(options.body),
    signal: options.signal,
  })

  return readJson<T>(response)
}

/** Reads a list plus the X-Total count the server sets for pagination. */
export async function requestPage<T>(
  path: string,
  query?: Record<string, QueryValue>,
  signal?: AbortSignal,
): Promise<Page<T>> {
  const response = await send(buildUrl(path, query), {
    headers: { Accept: 'application/json' },
    credentials: 'same-origin',
    signal,
  })
  const items = (await readJson<T[] | null>(response)) ?? []
  return { items, total: Number(response.headers.get('X-Total') ?? items.length) }
}
