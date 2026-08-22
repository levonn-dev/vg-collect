import { screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { jsonResponse, requestPath } from '../../test/fixtures'
import { renderWithMoney } from '../../test/money'
import ManualMatchPicker from './ManualMatchPicker'

afterEach(() => vi.unstubAllGlobals())

const listingAnswer = {
  degraded: false,
  results: [{
    type: 'pc_listing', name: 'Chrono Trigger [PAL]', pc_product_id: 7042,
    console_name: 'PAL Super Nintendo', loose_cents: 9800, cib_cents: 14200, new_cents: 30000,
  }],
}

function stubSearch() {
  const fetchMock = vi.fn().mockImplementation((url: unknown) =>
    requestPath(url).startsWith('/api/search')
      ? Promise.resolve(jsonResponse(200, listingAnswer))
      : Promise.resolve(jsonResponse(404, {})),
  )
  vi.stubGlobal('fetch', fetchMock)
  return fetchMock
}

it('searches listings only and hands back the picked one without resolving', async () => {
  const fetchMock = stubSearch()
  const onPick = vi.fn()
  renderWithMoney(<ManualMatchPicker initialQuery="" onPick={onPick} onClose={vi.fn()} />)
  // Single kind: no radio fieldset, and the box says so.
  expect(screen.queryByRole('radio')).not.toBeInTheDocument()
  await userEvent.type(screen.getByRole('searchbox', { name: 'Search for PriceCharting' }), 'chrono')
  await userEvent.click(screen.getByRole('button', { name: 'Search' }))
  await userEvent.click(await screen.findByRole('button', { name: /use chrono trigger/i }))
  expect(onPick).toHaveBeenCalledWith({ pcProductId: 7042, name: 'Chrono Trigger [PAL]' })
  // Search-only: the choice rides the game resolve later.
  expect(fetchMock.mock.calls.every((c) => requestPath(c[0]).startsWith('/api/search'))).toBe(true)
})

it('prefills and auto-fires the listing search from initialQuery', async () => {
  stubSearch()
  renderWithMoney(<ManualMatchPicker initialQuery="Chrono Trigger" onPick={vi.fn()} onClose={vi.fn()} />)
  expect(screen.getByRole('searchbox', { name: 'Search for PriceCharting' })).toHaveValue('Chrono Trigger')
  expect(await screen.findByRole('button', { name: /use chrono trigger/i })).toBeInTheDocument()
})

it('is a modal dialog, starts focus inside, and returns it on close', () => {
  stubSearch()
  const opener = document.createElement('button')
  document.body.appendChild(opener)
  opener.focus()
  const { unmount } = renderWithMoney(<ManualMatchPicker initialQuery="" onPick={vi.fn()} onClose={vi.fn()} />)
  const dialog = screen.getByRole('dialog', { name: 'Match a price listing' })
  expect(dialog).toHaveAttribute('aria-modal', 'true')
  expect(dialog).toContainElement(document.activeElement as HTMLElement)
  unmount()
  expect(document.activeElement).toBe(opener)
  opener.remove()
})

it('offers Close', async () => {
  stubSearch()
  const onClose = vi.fn()
  renderWithMoney(<ManualMatchPicker initialQuery="" onPick={vi.fn()} onClose={onClose} />)
  await userEvent.click(screen.getByRole('button', { name: 'Close' }))
  expect(onClose).toHaveBeenCalled()
})
