import { vi } from 'vitest'
import { centsToDollars, dollarsToCents, enteredCentsToUsdCents, formatCents, formatDate, formatMajor, isStaleRateDate, releaseYear, usdCentsToMajor } from './format'

it('formatCents renders currency and passes null through', () => {
  expect(formatCents(4200)).toBe('$42.00')
  expect(formatCents(999, 'EUR')).toContain('9.99')
  expect(formatCents(null)).toBeNull()
  expect(formatCents(undefined)).toBeNull()
})

it('formatCents honors an explicit locale', () => {
  expect(formatCents(123456, 'EUR', 'de-DE')).toBe('1.234,56 €')
  expect(formatCents(123456, 'EUR', 'en-US')).toBe('€1,234.56')
  expect(formatCents(50000, 'JPY', 'en-US')).toBe('¥500')
})

it('formatCents default locale: a stored choice outranks the browser language', () => {
  const langSpy = vi.spyOn(window.navigator, 'language', 'get').mockReturnValue('fr-FR')
  try {
    // No stored choice yet: browser language governs, fr-FR grouping
    // uses a comma decimal, not en's period.
    expect(formatCents(123456)).toContain(',56')
    expect(formatCents(123456)).not.toBe('$1,234.56')

    // A stored choice wins even though the browser still says fr-FR.
    localStorage.setItem('locale', 'en')
    expect(formatCents(123456)).toBe('$1,234.56')
  } finally {
    localStorage.removeItem('locale')
    langSpy.mockRestore()
  }
})

it('formatMajor renders major amounts, optionally whole-unit', () => {
  expect(formatMajor(21, 'EUR', 'en-US')).toBe('€21.00')
  expect(formatMajor(1234.56, 'USD', 'en-US', { wholeUnits: true })).toBe('$1,235')
})

it('converts between USD cents and display major units', () => {
  expect(usdCentsToMajor(4200, 0.5)).toBe(21)
  expect(usdCentsToMajor(4200, 1)).toBe(42)
  expect(enteredCentsToUsdCents(6000, 0.5)).toBe(12000)
  expect(enteredCentsToUsdCents(50000, 150)).toBe(333) // 500 JPY -> $3.33
  expect(enteredCentsToUsdCents(5999, 1)).toBe(5999)
})

it('isStaleRateDate flags a snapshot only once a full week has elapsed', () => {
  const oneWeekLater = new Date(Date.UTC(2026, 0, 8)) // 2026-01-01 + exactly 7 days
  expect(isStaleRateDate('2026-01-01', oneWeekLater)).toBe(false)
  const justOver = new Date(Date.UTC(2026, 0, 8, 0, 0, 1))
  expect(isStaleRateDate('2026-01-01', justOver)).toBe(true)
  expect(isStaleRateDate('not-a-date', oneWeekLater)).toBe(false)
  expect(isStaleRateDate('', oneWeekLater)).toBe(false)
})

it('formatDate renders an ISO timestamp through the given locale', () => {
  // Noon UTC sidesteps local-timezone date-shifting around midnight.
  expect(formatDate('1995-03-11T12:00:00Z', 'en-US')).toBe('3/11/1995')
  expect(formatDate('1995-03-11T12:00:00Z', 'de-DE')).toBe('11.3.1995')
})

it('releaseYear extracts the year', () => {
  expect(releaseYear('1995-03-11')).toBe('1995')
  expect(releaseYear(undefined)).toBeNull()
})

it('dollarsToCents round-trips with centsToDollars and rejects garbage', () => {
  expect(dollarsToCents('59.99')).toBe(5999)
  expect(dollarsToCents(' 10 ')).toBe(1000)
  expect(dollarsToCents('')).toBeUndefined()
  expect(dollarsToCents('abc')).toBeUndefined()
  expect(dollarsToCents('-5')).toBeUndefined()
  expect(centsToDollars(5999)).toBe('59.99')
  expect(centsToDollars(undefined)).toBe('')
  expect(dollarsToCents(centsToDollars(12345))).toBe(12345)
})
