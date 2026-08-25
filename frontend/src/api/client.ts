import createClient from 'openapi-fetch'
import type { paths } from './schema'

// fetch resolves through globalThis per call, never captured at init:
// OTel patches window.fetch after this module imports.
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

// Converts openapi-fetch's {data,error,response} into throw-on-problem:
// a parsed problem body carries code/detail, other non-ok is a bare
// status, 204 resolves undefined.
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
