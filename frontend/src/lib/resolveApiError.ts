import type { I18n, MessageDescriptor } from '@lingui/core'
import { ApiError } from '../api/client'

// resolveApiError turns a caught error into the string a UI banner
// shows: a caller-owned code lookup first, the server's own detail
// text next, and a caller-owned generic fallback last. Admin and
// account each repeated this exact three-step shape under a different
// name, each with its own near-identical copy of this explanation; i18n
// is threaded in explicitly (not read off useLingui()) because every
// caller is itself a plain function, not a component, so it cannot call
// useLingui() on its own behalf - the caller's own component does that
// and passes the result through.
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
