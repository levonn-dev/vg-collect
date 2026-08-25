import { vi } from 'vitest'
import { formatLocale } from './locale'
import { relativeTime } from './relativeTime'

it('buckets relative time from just-now through the week-plus date fallback', () => {
  const now = new Date('2026-07-25T12:00:00Z').getTime()
  expect(relativeTime(new Date(now - 30_000).toISOString(), now)).toBe('just now')
  expect(relativeTime(new Date(now - 5 * 60_000).toISOString(), now)).toBe('5m ago')
  expect(relativeTime(new Date(now - 3 * 3_600_000).toISOString(), now)).toBe('3h ago')
  expect(relativeTime(new Date(now - 2 * 86_400_000).toISOString(), now)).toBe('2d ago')
  const old = new Date(now - 10 * 86_400_000)
  expect(relativeTime(old.toISOString(), now)).toBe(old.toLocaleDateString())
})

it('pins the week-plus date fallback to the resolved locale, not the runtime default', () => {
  const langSpy = vi.spyOn(window.navigator, 'language', 'get').mockReturnValue('fr-FR')
  try {
    const now = new Date('2026-07-25T12:00:00Z').getTime()
    const old = new Date(now - 10 * 86_400_000)
    // fr-FR ("15/07/2026") differs from the runtime default
    // ("7/15/2026"); fails if the fallback stops routing through formatLocale().
    expect(relativeTime(old.toISOString(), now)).toBe(old.toLocaleDateString(formatLocale()))
  } finally {
    langSpy.mockRestore()
  }
})
