import { useQuery } from '@tanstack/react-query'
import { fetchMe } from '../api/client'
import type { Entry } from '../api/collection'
import { fetchFxRates } from '../api/fx'
import { formatCents, formatMajor, isStaleRateDate, usdCentsToMajor } from './format'

// useFxRates loads the daily rate snapshot. staleTime one hour: the
// upstream refreshes daily and the bff relays a cached snapshot, so
// refetching more often buys nothing.
export function useFxRates() {
  return useQuery({
    queryKey: ['fx'],
    queryFn: fetchFxRates,
    staleTime: 60 * 60 * 1000,
    refetchOnWindowFocus: false,
  })
}

export interface DisplayMoney {
  // The currency market values actually render in right now (USD
  // whenever rates cannot serve the profile currency).
  currency: string
  // The profile's raw preference, independent of rates. Stamping and
  // rate-free labels use this, never `currency`.
  profileCurrency: string
  // False while non-USD conversion cannot run; format falls back to
  // USD output.
  ready: boolean
  // Snapshot date, set only while actively converting (non-USD).
  rateDate?: string
  // True while actively converting from a snapshot older than a week;
  // USD and rates-down are never stale.
  rateStale: boolean
  // Target-units-per-USD for `currency`: 1 for USD, undefined while
  // not ready.
  rate?: number
  // Rate for an arbitrary code from the current snapshot (1 for USD,
  // undefined while rates are unavailable or the code is unrated).
  // The custom-price form converts at ITS OWN frozen input currency,
  // which can differ from the active display currency mid-edit.
  rateFor: (code: string) => number | undefined
  format: (usdCents: number | null | undefined) => string | null
  // Whole-unit variant for chart axes.
  format0: (usdCents: number | null | undefined) => string | null
  // The entry's market value with the custom-price pin rule: the
  // typed pair renders verbatim when its currency IS the display
  // currency; anything else converts the USD snapshot.
  entryValue: (e: Entry) => string | null
}

// useDisplayMoney is the single conversion point: every market-value
// render flows through it. USD stays canonical and short-circuits
// (never a rate lookup); price-paid amounts never come through here.
export function useDisplayMoney(): DisplayMoney {
  const me = useQuery({ queryKey: ['me'], queryFn: fetchMe })
  const fx = useFxRates()

  const profileCurrency = me.data?.preferred_currency ?? 'USD'
  const rate = profileCurrency === 'USD' ? 1 : fx.data?.rates[profileCurrency]
  const ready = rate !== undefined
  const currency = ready ? profileCurrency : 'USD'
  const active = ready && currency !== 'USD'

  const toDisplay = (
    usdCents: number | null | undefined,
    wholeUnits: boolean,
  ): string | null => {
    if (usdCents === null || usdCents === undefined) return null
    if (!active) return formatMajor(usdCents / 100, 'USD', undefined, { wholeUnits })
    return formatMajor(usdCentsToMajor(usdCents, rate), currency, undefined, { wholeUnits })
  }

  return {
    currency,
    profileCurrency,
    ready,
    rateDate: active ? fx.data?.date : undefined,
    rateStale: active && fx.data !== undefined && isStaleRateDate(fx.data.date),
    rate: ready ? rate : undefined,
    rateFor: (code) => (code === 'USD' ? 1 : fx.data?.rates[code]),
    format: (usdCents) => toDisplay(usdCents, false),
    format0: (usdCents) => toDisplay(usdCents, true),
    entryValue: (e) => {
      if (
        e.pricing_mode === 'custom' &&
        e.custom_value_entered_cents !== undefined &&
        e.custom_value_entered_currency !== undefined &&
        e.custom_value_entered_currency === currency
      ) {
        return formatCents(e.custom_value_entered_cents, e.custom_value_entered_currency)
      }
      return toDisplay(e.value_cents, false)
    },
  }
}
