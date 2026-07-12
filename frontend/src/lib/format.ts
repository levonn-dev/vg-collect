// The browser's own locale decides digit grouping and symbol
// placement (jsdom pins en-US, keeping unit tests deterministic).
// Tests pass an explicit locale; production code never does.
const defaultLocale = typeof navigator !== 'undefined' ? navigator.language : undefined

// formatCents renders integer cents as a currency string, or null for
// an absent value. Callers choose the neutral copy for null: an absent
// market value has several causes (pricing disabled, no match, no
// price for the packaging, catalog unreachable) and a single-entry
// read cannot distinguish them, so the UI must not guess one.
export function formatCents(
  cents: number | null | undefined,
  currency = 'USD',
  locale: string | undefined = defaultLocale,
): string | null {
  if (cents === null || cents === undefined) return null
  return new Intl.NumberFormat(locale, { style: 'currency', currency }).format(cents / 100)
}

// formatMajor renders an amount already in major units of the given
// currency. wholeUnits drops decimals for chart axes.
export function formatMajor(
  amount: number,
  currency: string,
  locale: string | undefined = defaultLocale,
  opts: { wholeUnits?: boolean } = {},
): string {
  return new Intl.NumberFormat(locale, {
    style: 'currency',
    currency,
    ...(opts.wholeUnits ? { minimumFractionDigits: 0, maximumFractionDigits: 0 } : {}),
  }).format(amount)
}

// usdCentsToMajor converts canonical USD cents into major units of
// the display currency; rate is target-units-per-USD. USD
// short-circuits at call sites (rate 1 keeps this an identity).
export function usdCentsToMajor(usdCents: number, rate: number): number {
  return (usdCents / 100) * rate
}

// enteredCentsToUsdCents converts a typed amount (minor units of the
// entered currency) into the canonical USD-cents snapshot. One round
// at the boundary; never round-trip a stored value back through.
export function enteredCentsToUsdCents(enteredCents: number, rate: number): number {
  return Math.round(enteredCents / rate)
}

// isStaleRateDate flags a rate snapshot older than a week. The ECB
// publishes each business day; weekends and multi-day holiday closures
// keep normal gaps under five days, so only a real outage trips this.
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

// dollarsToCents parses a money text input ("59.99") into integer
// cents; empty or invalid input is undefined (field unset).
export function dollarsToCents(text: string): number | undefined {
  const trimmed = text.trim()
  if (trimmed === '') return undefined
  const value = Number(trimmed)
  if (!Number.isFinite(value) || value < 0) return undefined
  return Math.round(value * 100)
}

// centsToDollars renders integer cents as a plain decimal string for
// form inputs ("59.99"), empty for unset.
export function centsToDollars(cents: number | null | undefined): string {
  if (cents === null || cents === undefined) return ''
  return (cents / 100).toFixed(2)
}
