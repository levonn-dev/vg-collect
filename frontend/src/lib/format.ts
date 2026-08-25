import { formatLocale } from './locale'

// Null for an absent value: several causes (pricing disabled, no
// match, unreachable) a single read can't distinguish, so callers
// choose the neutral copy.
export function formatCents(
  cents: number | null | undefined,
  currency = 'USD',
  locale: string | undefined = formatLocale(),
): string | null {
  if (cents === null || cents === undefined) return null
  return new Intl.NumberFormat(locale, { style: 'currency', currency }).format(cents / 100)
}

// Amount already in major units; wholeUnits drops decimals for chart axes.
export function formatMajor(
  amount: number,
  currency: string,
  locale: string | undefined = formatLocale(),
  opts: { wholeUnits?: boolean } = {},
): string {
  return new Intl.NumberFormat(locale, {
    style: 'currency',
    currency,
    ...(opts.wholeUnits ? { minimumFractionDigits: 0, maximumFractionDigits: 0 } : {}),
  }).format(amount)
}

// Canonical USD cents to major units of the display currency; rate is
// target-units-per-USD (1 = USD identity).
export function usdCentsToMajor(usdCents: number, rate: number): number {
  return (usdCents / 100) * rate
}

// Typed amount to canonical USD-cents. Rounds once at the boundary;
// never round-trip a stored value back through.
export function enteredCentsToUsdCents(enteredCents: number, rate: number): number {
  return Math.round(enteredCents / rate)
}

// Flags a snapshot over a week old. ECB publishes each business day;
// normal gaps stay under 5 days.
export function isStaleRateDate(date: string, now: Date = new Date()): boolean {
  const [y, m, d] = date.split('-').map(Number)
  if (!y || !m || !d) return false
  return now.getTime() - Date.UTC(y, m - 1, d) > 7 * 24 * 60 * 60 * 1000
}

// releaseYear reduces an ISO date to its year for compact display.
export function releaseYear(date: string | null | undefined): string | null {
  if (!date) return null
  return date.slice(0, 4)
}

// Shared idiom for every "when did this happen" byline (admin tables,
// account/pricing timestamps, shelf dates).
export function formatDate(iso: string, locale: string | undefined = formatLocale()): string {
  return new Date(iso).toLocaleDateString(locale)
}

// Parses money text ("59.99") into integer cents; empty/invalid is undefined.
export function dollarsToCents(text: string): number | undefined {
  const trimmed = text.trim()
  if (trimmed === '') return undefined
  const value = Number(trimmed)
  if (!Number.isFinite(value) || value < 0) return undefined
  return Math.round(value * 100)
}

// Integer cents to a plain decimal string ("59.99") for form inputs;
// empty for unset.
export function centsToDollars(cents: number | null | undefined): string {
  if (cents === null || cents === undefined) return ''
  return (cents / 100).toFixed(2)
}
