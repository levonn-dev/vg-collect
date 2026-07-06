// formatCents renders integer cents as a currency string, or null for
// an absent value. Callers choose the neutral copy for null: an absent
// market value has several causes (pricing disabled, no match, no
// price for the packaging, catalog unreachable) and a single-entry
// read cannot distinguish them, so the UI must not guess one.
export function formatCents(cents: number | null | undefined, currency = 'USD'): string | null {
  if (cents === null || cents === undefined) return null
  return new Intl.NumberFormat('en-US', { style: 'currency', currency }).format(cents / 100)
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
