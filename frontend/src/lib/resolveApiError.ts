import type { I18n, MessageDescriptor } from '@lingui/core'
import { ApiError } from '../api/client'

// Code lookup, then server detail text, then a generic fallback. i18n
// is threaded explicitly since callers are plain functions, not
// components, and can't call useLingui() themselves.
export function resolveApiError(
  e: unknown,
  i18n: I18n,
  codes: Record<string, MessageDescriptor>,
  fallback: MessageDescriptor,
): string {
  if (e instanceof ApiError) {
    const known = e.code ? codes[e.code] : undefined
    if (known) return i18n._(known)
    if (e.message) return e.message
  }
  return i18n._(fallback)
}
