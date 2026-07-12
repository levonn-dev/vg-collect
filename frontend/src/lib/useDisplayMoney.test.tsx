import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { renderHook } from '@testing-library/react'
import type { ReactNode } from 'react'
import type { FXRates } from '../api/fx'
import { entryFixture, fxRatesFixture, meFixture } from '../test/fixtures'
import { useDisplayMoney } from './useDisplayMoney'

// Today's fixture date, computed once so every assertion in this file
// compares against the same string the default fixture produced.
const today = new Date().toISOString().slice(0, 10)

function wrapper(currency: string, rates: boolean, fxOverrides: Partial<FXRates> = {}) {
  const qc = new QueryClient({
    defaultOptions: { queries: { retry: false, staleTime: Infinity } },
  })
  qc.setQueryData(['me'], meFixture({ preferred_currency: currency }))
  if (rates) qc.setQueryData(['fx'], fxRatesFixture(fxOverrides))
  return ({ children }: { children: ReactNode }) => (
    <QueryClientProvider client={qc}>{children}</QueryClientProvider>
  )
}

function money(currency: string, rates = true, fxOverrides: Partial<FXRates> = {}) {
  return renderHook(() => useDisplayMoney(), { wrapper: wrapper(currency, rates, fxOverrides) }).result.current
}

it('USD is the identity: no rate lookup, plain formatting', () => {
  const m = money('USD')
  expect(m.currency).toBe('USD')
  expect(m.ready).toBe(true)
  expect(m.rate).toBe(1)
  expect(m.rateDate).toBeUndefined()
  expect(m.rateStale).toBe(false)
  expect(m.format(4200)).toBe('$42.00')
  expect(m.format(null)).toBeNull()
})

it('converts USD cents into the profile currency', () => {
  const m = money('EUR')
  expect(m.currency).toBe('EUR')
  expect(m.rate).toBe(0.5)
  expect(m.rateDate).toBe(today)
  expect(m.rateStale).toBe(false)
  expect(m.format(4200)).toBe('€21.00')
  expect(m.format0(4200)).toBe('€21')
})

it('falls back to USD display while rates cannot serve the currency', () => {
  const m = money('EUR', false)
  expect(m.currency).toBe('USD')
  expect(m.profileCurrency).toBe('EUR')
  expect(m.ready).toBe(false)
  expect(m.rate).toBeUndefined()
  expect(m.rateStale).toBe(false)
  expect(m.format(4200)).toBe('$42.00')
})

it('rateStale flags a snapshot older than a week, only while actively converting', () => {
  expect(money('EUR', true, { date: '2026-01-01' }).rateStale).toBe(true)
  expect(money('USD', true, { date: '2026-01-01' }).rateStale).toBe(false) // USD never converts
  expect(money('EUR', false).rateStale).toBe(false) // rates down, not "stale"
})

it('rateFor looks up an arbitrary code from the snapshot, independent of the active display currency', () => {
  const m = money('USD')
  expect(m.rateFor('USD')).toBe(1)
  expect(m.rateFor('EUR')).toBe(0.5)
  expect(m.rateFor('XXX')).toBeUndefined() // fx present, code unrated
  expect(money('EUR', false).rateFor('EUR')).toBeUndefined() // fx absent
})

it('pins the typed pair when its currency matches the display currency', () => {
  const m = money('EUR')
  const pinned = entryFixture({
    pricing_mode: 'custom',
    value_cents: 11900, // drifted USD snapshot
    custom_value_cents: 11900,
    custom_value_entered_cents: 6000,
    custom_value_entered_currency: 'EUR',
  })
  expect(m.entryValue(pinned)).toBe('€60.00')

  const otherCurrency = entryFixture({
    pricing_mode: 'custom',
    value_cents: 11900,
    custom_value_cents: 11900,
    custom_value_entered_cents: 6000,
    custom_value_entered_currency: 'GBP',
  })
  expect(m.entryValue(otherCurrency)).toBe('€59.50') // 11900 * 0.5 / 100

  const noPair = entryFixture({ pricing_mode: 'auto', value_cents: 4200 })
  expect(m.entryValue(noPair)).toBe('€21.00')
})
