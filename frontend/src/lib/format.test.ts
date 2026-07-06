import { centsToDollars, dollarsToCents, formatCents, releaseYear } from './format'

it('formatCents renders currency and passes null through', () => {
  expect(formatCents(4200)).toBe('$42.00')
  expect(formatCents(999, 'EUR')).toContain('9.99')
  expect(formatCents(null)).toBeNull()
  expect(formatCents(undefined)).toBeNull()
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
