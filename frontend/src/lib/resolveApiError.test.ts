import { i18n } from '@lingui/core'
import type { MessageDescriptor } from '@lingui/core'
import { ApiError } from '../api/client'
import { messages } from '../locales/en.po'
import { resolveApiError } from './resolveApiError'

i18n.load('en', messages)
i18n.activate('en')

// Plain object literals, not the msg`...` macro: this file's fixture
// text is not real UI copy, and the macro would extract it into the
// shared catalog (lingui scans every file under src, tests included)
// for no reader to ever translate. {id, message} is exactly what msg
// compiles a literal (no ICU/interpolation) string down to, so this is
// the same shape resolveApiError's real callers pass, minus extraction.
const codes: Record<string, MessageDescriptor> = {
  known_code: { id: 'Known problem.', message: 'Known problem.' },
}
const fallback: MessageDescriptor = { id: 'Generic failure.', message: 'Generic failure.' }

test('a known code returns its own translated message', () => {
  const e = new ApiError(409, 'known_code', 'server detail')
  expect(resolveApiError(e, i18n, codes, fallback)).toBe('Known problem.')
})

test('an unmatched code falls back to the server detail message', () => {
  const e = new ApiError(500, 'unknown_code', 'server detail')
  expect(resolveApiError(e, i18n, codes, fallback)).toBe('server detail')
})

test('an unmatched code with no server detail falls back to the generic message', () => {
  // '' is explicit: an omitted detail synthesizes a non-empty
  // ApiError.message (see the next test), so the only way to reach the
  // generic fallback with an ApiError at all is a server response that
  // sends the empty string as its own detail.
  const e = new ApiError(500, 'unknown_code', '')
  expect(resolveApiError(e, i18n, codes, fallback)).toBe('Generic failure.')
})

test('an ApiError with no code at all falls back to the server detail message', () => {
  const e = new ApiError(500)
  // The ApiError constructor synthesizes "request failed with status
  // 500" as Error.message when no detail is given - still a truthy
  // message, so it still wins over the generic fallback.
  expect(resolveApiError(e, i18n, codes, fallback)).toBe('request failed with status 500')
})

test('a non-ApiError value always falls back to the generic message', () => {
  expect(resolveApiError(new Error('network down'), i18n, codes, fallback)).toBe('Generic failure.')
  expect(resolveApiError('not even an Error', i18n, codes, fallback)).toBe('Generic failure.')
})
