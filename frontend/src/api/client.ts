import createClient from 'openapi-fetch'
import type { paths } from './schema'

// fetch resolves through globalThis on every call, never captured at
// module init: the OTel fetch instrumentation patches window.fetch
// well after this module is imported, and a captured reference would
// send every API call around it.
export const api = createClient<paths>({ fetch: (input) => globalThis.fetch(input) })

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

// unwrap converts openapi-fetch's { data, error, response } answer into
// the throw-on-problem contract the facade has always had: a parsed
// problem body rejects with ApiError carrying its code/detail,
// anything else non-ok rejects with a bare ApiError(status), and 204
// resolves undefined.
export function unwrap<D>(result: {
  data?: D
  error?: { code?: string; detail?: string }
  response: Response
}): Promise<D> {
  const { data, error, response } = result
  if (!response.ok) {
    return Promise.reject(new ApiError(response.status, error?.code, error?.detail))
  }
  return Promise.resolve(data as D)
}
