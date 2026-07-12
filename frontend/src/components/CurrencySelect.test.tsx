import { screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { jsonResponse, meFixture } from '../test/fixtures'
import { renderWithMoney } from '../test/money'
import CurrencySelect from './CurrencySelect'

afterEach(() => vi.unstubAllGlobals())

it('lists USD plus every rated currency, sorted', () => {
  renderWithMoney(<CurrencySelect />)
  const select = screen.getByLabelText('Display currency')
  const options = Array.from(select.querySelectorAll('option')).map((o) => o.value)
  expect(options).toEqual(['USD', 'EUR', 'GBP', 'JPY'])
})

it('saves the choice optimistically and PATCHes the profile', async () => {
  const fetchMock = vi.fn().mockResolvedValue(
    jsonResponse(200, meFixture({ preferred_currency: 'EUR' })),
  )
  vi.stubGlobal('fetch', fetchMock)
  const { queryClient } = renderWithMoney(<CurrencySelect />)

  await userEvent.selectOptions(screen.getByLabelText('Display currency'), 'EUR')

  // Optimistic: the cache flips before the PATCH settles.
  expect(
    (queryClient.getQueryData(['me']) as { preferred_currency: string }).preferred_currency,
  ).toBe('EUR')
  const patch = fetchMock.mock.calls.find(
    (c) => (c[1] as RequestInit | undefined)?.method === 'PATCH',
  )
  expect(patch?.[0]).toBe('/api/me')
  expect(JSON.parse((patch?.[1] as { body: string }).body)).toEqual({
    preferred_currency: 'EUR',
  })
})

it('rolls the cache back when the save fails', async () => {
  const fetchMock = vi.fn().mockResolvedValue(
    jsonResponse(500, { code: 'internal', detail: 'nope' }),
  )
  vi.stubGlobal('fetch', fetchMock)
  const { queryClient } = renderWithMoney(<CurrencySelect />)

  await userEvent.selectOptions(screen.getByLabelText('Display currency'), 'EUR')
  await waitFor(() =>
    expect(
      (queryClient.getQueryData(['me']) as { preferred_currency: string }).preferred_currency,
    ).toBe('USD'),
  )
  expect(await screen.findByRole('alert')).toHaveTextContent('Saving failed. Please try again.')
})

it('is pinned to USD and disabled while rates are unavailable', () => {
  renderWithMoney(<CurrencySelect />, { rates: false })
  const select: HTMLSelectElement = screen.getByLabelText('Display currency')
  expect(select.disabled).toBe(true)
  expect(select.title).toBe('Exchange rates are unavailable; prices show in USD.')
  const options = Array.from(select.querySelectorAll('option')).map((o) => o.value)
  expect(options).toEqual(['USD'])
})
