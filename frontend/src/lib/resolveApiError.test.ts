import { i18n } from '@lingui/core'
import type { MessageDescriptor } from '@lingui/core'
import { ApiError } from '../api/client'
import { messages } from '../locales/en.po'
import { resolveApiError } from './resolveApiError'

i18n.load('en', messages)
i18n.activate('en')

// Plain object literals, not msg macro: fixture text isn't real UI
// copy and the macro would extract it into the shared catalog.
// {id, message} matches what msg compiles a literal string to.
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
  // '' explicit: an omitted detail synthesizes a non-empty message
  // (next test), so only an explicit empty detail reaches the fallback.
  const e = new ApiError(500, 'unknown_code', '')
  expect(resolveApiError(e, i18n, codes, fallback)).toBe('Generic failure.')
})

test('an ApiError with no code at all falls back to the server detail message', () => {
  const e = new ApiError(500)
  // Constructor synthesizes "request failed with status 500" when no
  // detail is given; still truthy, still wins over the fallback.
  expect(resolveApiError(e, i18n, codes, fallback)).toBe('request failed with status 500')
})

test('a non-ApiError value always falls back to the generic message', () => {
  expect(resolveApiError(new Error('network down'), i18n, codes, fallback)).toBe('Generic failure.')
  expect(resolveApiError('not even an Error', i18n, codes, fallback)).toBe('Generic failure.')
})
