import { i18n } from '@lingui/core'
import { I18nProvider } from '@lingui/react'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { render } from '@testing-library/react'
import type { ReactElement } from 'react'
import { fxRatesFixture, meFixture } from './fixtures'

// renderWithMoney renders under a QueryClientProvider whose cache is
// pre-seeded with the profile and the rate snapshot, so components
// using useDisplayMoney resolve synchronously with no fetch stubs.
// staleTime Infinity stops the seeded queries from refetching (there
// is no fetch stub to answer them). rates: false simulates the
// rates-unavailable fallback.
export function renderWithMoney(
  ui: ReactElement,
  opts: { currency?: string; rates?: boolean } = {},
) {
  const queryClient = new QueryClient({
    defaultOptions: { queries: { retry: false, staleTime: Infinity } },
  })
  queryClient.setQueryData(['me'], meFixture({ preferred_currency: opts.currency ?? 'USD' }))
  if (opts.rates !== false) queryClient.setQueryData(['fx'], fxRatesFixture())
  const result = render(
    <I18nProvider i18n={i18n}>
      <QueryClientProvider client={queryClient}>{ui}</QueryClientProvider>
    </I18nProvider>,
  )
  return { ...result, queryClient }
}
