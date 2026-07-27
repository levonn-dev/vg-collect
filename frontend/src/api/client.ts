import type { paths } from './schema'

export type Me =
  paths['/api/me']['get']['responses']['200']['content']['application/json']
type Providers =
  paths['/api/auth/providers']['get']['responses']['200']['content']['application/json']
export type Identity =
  paths['/api/me/identities']['get']['responses']['200']['content']['application/json']['identities'][number]
type Identities =
  paths['/api/me/identities']['get']['responses']['200']['content']['application/json']
type UpdateMeRequest = NonNullable<
  paths['/api/me']['patch']['requestBody']
>['content']['application/json']

// ApiError carries the RFC 9457 problem fields the UI branches on.
export class ApiError extends Error {
  readonly status: number
  readonly code?: string

  constructor(status: number, code?: string, detail?: string) {
    super(detail ?? `request failed with status ${status}`)
    this.name = 'ApiError'
    this.status = status
    this.code = code
  }
}

async function toApiError(res: Response): Promise<ApiError> {
  try {
    const body = (await res.json()) as { code?: string; detail?: string }
    return new ApiError(res.status, body.code, body.detail)
  } catch {
    return new ApiError(res.status)
  }
}

export async function getJSON<T>(path: string): Promise<T> {
  const res = await fetch(path)
  if (!res.ok) throw await toApiError(res)
  return (await res.json()) as T
}

// sendJSON issues a mutating call. 204 answers resolve to undefined;
// callers with a body type parameterize T accordingly. opts is an
// optional trailing bag (currently just keepalive, for callers that
// must survive the tab closing mid-request, e.g. a beacon-style
// commit on pagehide/unmount) - additive so every existing call site
// keeps compiling unchanged.
export async function sendJSON<T>(
  method: 'POST' | 'PUT' | 'PATCH' | 'DELETE',
  path: string,
  body?: unknown,
  opts?: { keepalive?: boolean },
): Promise<T> {
  const res = await fetch(path, {
    method,
    ...(body === undefined
      ? {}
      : { headers: { 'Content-Type': 'application/json' }, body: JSON.stringify(body) }),
    ...(opts?.keepalive === undefined ? {} : { keepalive: opts.keepalive }),
  })
  if (!res.ok) throw await toApiError(res)
  if (res.status === 204) return undefined as T
  return (await res.json()) as T
}

export function fetchMe(): Promise<Me> {
  return getJSON<Me>('/api/me')
}

export async function fetchProviders(): Promise<string[]> {
  const body = await getJSON<Providers>('/api/auth/providers')
  return body.providers
}

export async function logout(): Promise<void> {
  const res = await fetch('/api/auth/logout', { method: 'POST' })
  if (!res.ok) throw await toApiError(res)
}

export function updateMe(body: UpdateMeRequest): Promise<Me> {
  return sendJSON<Me>('PATCH', '/api/me', body)
}

export async function fetchIdentities(): Promise<Identity[]> {
  const body = await getJSON<Identities>('/api/me/identities')
  return body.identities
}

export function unlinkIdentity(id: string): Promise<void> {
  return sendJSON<void>('DELETE', `/api/me/identities/${id}`)
}

export function deleteAccount(): Promise<void> {
  return sendJSON<void>('DELETE', '/api/me')
}
