import { act, cleanup, fireEvent, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import type { Entry } from '../../api/collection'
import { centsToDollars } from '../../lib/format'
import { entryFixture, fxRatesFixture, jsonResponse, putBody } from '../../test/fixtures'
import { renderWithMoney } from '../../test/money'
import type { PricingValue } from './PricingPanel'
import PricingPanel from './PricingPanel'

const matchedProduct = {
  id: 'p1', type: 'game', name: 'Chrono Trigger',
  pricecharting: {
    pc_product_id: 55, pc_name: 'Chrono Trigger', console_name: 'Super Nintendo',
    match_confidence: 0.93, verified: true,
    loose_cents: 1500, cib_cents: 4200, new_cents: 9900, as_of: '2026-07-01T00:00:00Z',
  },
  created_at: 'x', updated_at: 'x',
}

// A game product with no PriceCharting mapping yet, but a full
// identity (igdb + platform) - the shape the "Match listing" gate
// needs to consider the entry a re-match candidate.
const unmatchedGameProduct = {
  id: 'p1', type: 'game', name: 'Chrono Trigger',
  igdb: { game_id: 1000 },
  platform: { igdb_platform_id: 6, name: 'SNES' },
  created_at: 'x', updated_at: 'x',
}

function stubFetch(handlers: Record<string, unknown>) {
  const fetchMock = vi.fn().mockImplementation((url: string) => {
    const u = String(url)
    for (const [prefix, body] of Object.entries(handlers)) {
      if (u.startsWith(prefix)) return Promise.resolve(jsonResponse(200, body))
    }
    return Promise.resolve(jsonResponse(404, {
      type: 'about:blank', title: 'Not Found', status: 404, code: 'unknown_pricing_product',
      detail: 'no such pricing product in the catalog',
    }))
  })
  vi.stubGlobal('fetch', fetchMock)
  return fetchMock
}

// The panel is a controlled editor of the form's pricing draft; by
// default the draft mirrors the entry, like the form initializes it.
function renderPanel(
  entry: Entry = entryFixture(),
  value: PricingValue = {
    mode: entry.pricing_mode,
    productId: entry.pricing_product_id,
    customValue: centsToDollars(entry.custom_value_cents),
  },
  currency = 'USD',
) {
  const onChange = vi.fn()
  renderWithMoney(
    <PricingPanel entry={entry} value={value} onChange={onChange} inputCurrency={currency} />,
    { currency },
  )
  return onChange
}

afterEach(() => vi.unstubAllGlobals())

it('renders the absent value neutrally, without guessing a cause', () => {
  stubFetch({ '/api/products/': matchedProduct })
  renderPanel(entryFixture({ value_cents: undefined }))
  expect(screen.getByText('No market value available.')).toBeInTheDocument()
})

it('labels market values as USD', () => {
  stubFetch({ '/api/products/': matchedProduct })
  renderPanel(entryFixture({ value_cents: 4200 }))
  expect(screen.getByText('Market values are in USD.')).toBeInTheDocument()
})

it('converts the match quotes and names the rate snapshot', async () => {
  stubFetch({ '/api/products/': matchedProduct })
  renderPanel(entryFixture(), undefined, 'EUR')
  expect(await screen.findByText(/loose €7\.50/i)).toBeInTheDocument() // 1500 * 0.5
  expect(screen.getByText(/converted from usd at ecb rates \(\d{4}-\d{2}-\d{2}\)/i)).toBeInTheDocument()
  expect(screen.queryByText(/more than a week old/i)).not.toBeInTheDocument()
  expect(screen.queryByText('Market values are in USD.')).not.toBeInTheDocument()
})

it('flags the rate snapshot as stale once it is more than a week old', async () => {
  stubFetch({ '/api/products/': matchedProduct })
  const entry = entryFixture()
  const value: PricingValue = {
    mode: entry.pricing_mode,
    productId: entry.pricing_product_id,
    customValue: centsToDollars(entry.custom_value_cents),
  }
  const { queryClient } = renderWithMoney(
    <PricingPanel entry={entry} value={value} onChange={vi.fn()} inputCurrency="EUR" />,
    { currency: 'EUR' },
  )
  await act(() => queryClient.setQueryData(['fx'], fxRatesFixture({ date: '2026-01-01' })))
  expect(await screen.findByText(/more than a week old/i)).toBeInTheDocument()
})

it('keeps the plain USD note under USD', () => {
  stubFetch({ '/api/products/': matchedProduct })
  renderPanel()
  expect(screen.getByText('Market values are in USD.')).toBeInTheDocument()
})

it('states the fallback while rates are unavailable', () => {
  stubFetch({})
  const onChange = vi.fn()
  renderWithMoney(
    <PricingPanel
      entry={entryFixture()}
      value={{ mode: 'auto', productId: undefined, customValue: '' }}
      onChange={onChange}
      inputCurrency="USD"
    />,
    { currency: 'EUR', rates: false },
  )
  expect(screen.getByText(/exchange rates are unavailable; values show in usd/i)).toBeInTheDocument()
})

it('pins the entry value from a matching entered pair', () => {
  stubFetch({})
  const entry = entryFixture({
    pricing_mode: 'custom',
    product_id: undefined,
    value_cents: 11900,
    custom_value_cents: 11900,
    custom_value_entered_cents: 6000,
    custom_value_entered_currency: 'EUR',
  })
  renderPanel(entry, undefined, 'EUR')
  expect(screen.getByText('€60.00')).toBeInTheDocument()
})

it('shows the match card in auto mode', async () => {
  stubFetch({ '/api/products/': matchedProduct })
  renderPanel(entryFixture({ pricing_mode: 'auto', value_cents: 4200 }))
  expect(await screen.findByText(/match 93%/i)).toBeInTheDocument()
  expect(screen.getByText(/verified/i)).toBeInTheDocument()
  expect(screen.getByText('$42.00')).toBeInTheDocument()
})

it('shows match-pending when the product is unmatched', async () => {
  stubFetch({ '/api/products/': { ...matchedProduct, pricecharting: undefined } })
  renderPanel(entryFixture({ pricing_mode: 'auto' }))
  expect(await screen.findByText(/no confirmed price listing yet/i)).toBeInTheDocument()
})

it('reports a mode change as draft state and never saves on its own', async () => {
  const fetchMock = stubFetch({ '/api/products/': matchedProduct })
  const onChange = renderPanel(entryFixture({ pricing_mode: 'proxy', pricing_product_id: 'p9' }))
  await userEvent.click(screen.getByRole('radio', { name: /disabled/i }))
  // The parked proxy id survives the mode change (memory, not erasure).
  expect(onChange).toHaveBeenCalledWith({ mode: 'disabled', productId: 'p9', customValue: '' })
  const puts = fetchMock.mock.calls.filter((c) => (c[1] as RequestInit | undefined)?.method === 'PUT')
  expect(puts).toHaveLength(0)
})

it('presents a parked target as memory with a reactivate affordance', async () => {
  stubFetch({ '/api/products/': matchedProduct })
  const onChange = renderPanel(
    entryFixture({ pricing_mode: 'disabled', pricing_product_id: 'p9', product_id: undefined }),
  )
  expect(await screen.findByText(/last price proxy/i)).toBeInTheDocument()
  // Distinct accessible name: the custom-price memory row below can
  // render at once with its own "Reactivate" button, so a screen
  // reader (and a strict query) needs a name that says which.
  const reactivateButton = screen.getByRole('button', { name: 'Reactivate the last price proxy' })
  await userEvent.click(reactivateButton)
  expect(onChange).toHaveBeenCalledWith({ mode: 'proxy', productId: 'p9', customValue: '' })
})

it('offers auto only to product-backed entries and opens the picker for a first proxy', async () => {
  stubFetch({ '/api/products/': matchedProduct })
  const onChange = renderPanel(
    entryFixture({ product_id: undefined, pricing_mode: 'disabled', pricing_product_id: undefined }),
  )
  expect(screen.queryByRole('radio', { name: /^auto/i })).not.toBeInTheDocument()
  await userEvent.click(screen.getByRole('radio', { name: /proxy/i }))
  expect(await screen.findByRole('dialog', { name: /choose a price source/i })).toBeInTheDocument()
  expect(onChange).toHaveBeenCalledWith({ mode: 'proxy', productId: undefined, customValue: '' })
})

it('guides an unchosen proxy source and reopens the picker on demand', async () => {
  stubFetch({ '/api/products/': matchedProduct })
  renderPanel(
    entryFixture({ product_id: undefined, pricing_mode: 'disabled', pricing_product_id: undefined }),
    { mode: 'proxy', productId: undefined, customValue: '' },
  )
  expect(screen.getByText('No price source chosen yet.')).toBeInTheDocument()
  await userEvent.click(screen.getByRole('button', { name: /choose price source/i }))
  expect(await screen.findByRole('dialog', { name: /choose a price source/i })).toBeInTheDocument()
})

it('opens the price-source picker prefilled with the entry title and edition', async () => {
  stubFetch({
    '/api/products/': matchedProduct,
    '/api/search': { degraded: false, results: [] },
  })
  const entry = entryFixture({
    display_name: 'Super Mario 64', edition: "Player's Choice",
    product_id: undefined, pricing_mode: 'disabled', pricing_product_id: undefined,
  })
  renderPanel(entry, { mode: 'proxy', productId: undefined, customValue: '' })
  await userEvent.click(screen.getByRole('button', { name: /choose price source/i }))
  expect(
    await screen.findByRole('searchbox', { name: /search for games, hardware, and pricecharting/i }),
  ).toHaveValue("Super Mario 64 Player's Choice")
})

it('drives a fresh proxy pick through resolve and reports the resolved target', async () => {
  const resolved = { id: 'p7', type: 'game', name: 'Chrono Trigger', created_at: 'x', updated_at: 'x' }
  stubFetch({
    '/api/search': {
      degraded: false,
      results: [{ type: 'game', name: 'Chrono Trigger', igdb_game_id: 1000, platforms: [{ igdb_platform_id: 6, name: 'SNES' }] }],
    },
    '/api/products/resolve': resolved,
  })
  const onChange = renderPanel(
    entryFixture({ pricing_mode: 'disabled', pricing_product_id: undefined }),
    { mode: 'proxy', productId: undefined, customValue: '' },
  )
  await userEvent.click(screen.getByRole('button', { name: /choose price source/i }))
  await userEvent.type(
    await screen.findByRole('searchbox', { name: /search for games, hardware, and pricecharting/i }),
    'chrono',
  )
  await userEvent.click(screen.getByRole('button', { name: 'Search' }))
  await userEvent.click(await screen.findByRole('button', { name: 'Chrono Trigger on SNES' }))
  await waitFor(() => expect(onChange).toHaveBeenCalledWith({ mode: 'proxy', productId: 'p7', customValue: '' }))
})

it('offers auto/proxy/custom/disabled to product-backed entries and proxy/custom/disabled to off-catalog entries', () => {
  stubFetch({ '/api/products/': matchedProduct })
  renderPanel(entryFixture({ pricing_mode: 'auto' }))
  expect(screen.getAllByRole('radio')).toHaveLength(4)
  expect(screen.getByRole('radio', { name: /^auto/i })).toBeInTheDocument()
  expect(screen.getByRole('radio', { name: /^proxy/i })).toBeInTheDocument()
  expect(screen.getByRole('radio', { name: /^custom/i })).toBeInTheDocument()
  expect(screen.getByRole('radio', { name: /^disabled/i })).toBeInTheDocument()

  cleanup()
  renderPanel(entryFixture({ product_id: undefined, pricing_mode: 'disabled', pricing_product_id: undefined }))
  expect(screen.getAllByRole('radio')).toHaveLength(3)
  expect(screen.queryByRole('radio', { name: /^auto/i })).not.toBeInTheDocument()
  expect(screen.getByRole('radio', { name: /^proxy/i })).toBeInTheDocument()
  expect(screen.getByRole('radio', { name: /^custom/i })).toBeInTheDocument()
  expect(screen.getByRole('radio', { name: /^disabled/i })).toBeInTheDocument()
})

it('drafts a typed custom price as text, with no server call', () => {
  const fetchMock = stubFetch({ '/api/products/': matchedProduct })
  const value: PricingValue = { mode: 'custom', productId: undefined, customValue: '' }
  const onChange = renderPanel(entryFixture({ pricing_mode: 'custom', product_id: undefined }), value)
  const input = screen.getByLabelText(/custom price \(usd\)/i)
  fireEvent.change(input, { target: { value: '59.99' } })
  expect(onChange).toHaveBeenCalledWith({ mode: 'custom', productId: undefined, customValue: '59.99' })
  const puts = fetchMock.mock.calls.filter((c) => (c[1] as RequestInit | undefined)?.method === 'PUT')
  expect(puts).toHaveLength(0)
})

it('shows when the custom price was set', () => {
  stubFetch({ '/api/products/': matchedProduct })
  const entry = entryFixture({
    pricing_mode: 'custom', product_id: undefined,
    custom_value_cents: 5999, custom_value_set_at: '2026-07-01T00:00:00Z',
  })
  renderPanel(entry, { mode: 'custom', productId: undefined, customValue: '59.99' })
  expect(screen.getByText(/price set on/i)).toBeInTheDocument()
})

it('remembers the last custom price under another mode with a reactivate affordance', async () => {
  stubFetch({ '/api/products/': matchedProduct })
  const entry = entryFixture({ pricing_mode: 'disabled', pricing_product_id: undefined })
  const value: PricingValue = { mode: 'disabled', productId: undefined, customValue: '12.00' }
  const onChange = renderPanel(entry, value)
  expect(screen.getByText(/last custom price: \$12\.00/i)).toBeInTheDocument()
  const reactivateButton = screen.getByRole('button', { name: 'Reactivate the last custom price' })
  await userEvent.click(reactivateButton)
  expect(onChange).toHaveBeenCalledWith({ mode: 'custom', productId: undefined, customValue: '12.00' })
})

it('labels the custom input and the memory row in the input currency', () => {
  stubFetch({})
  const entry = entryFixture({ pricing_mode: 'custom', product_id: undefined })
  renderPanel(entry, { mode: 'custom', productId: undefined, customValue: '60' }, 'EUR')
  expect(screen.getByLabelText(/custom price \(eur\)/i)).toBeInTheDocument()
})

it('shows Match listing only for auto mode on an unmatched game product', async () => {
  // auto + unmatched game (igdb + platform present, no pricecharting) -> visible
  stubFetch({ '/api/products/': unmatchedGameProduct })
  renderPanel(entryFixture({ pricing_mode: 'auto' }))
  expect(await screen.findByRole('button', { name: 'Match listing' })).toBeInTheDocument()
  cleanup()

  // stored mode proxy, draft flipped to auto, same unmatched game with
  // full identity -> absent. The immediate PUT resends the STORED
  // entry, so the affordance needs the saved mode to already be auto,
  // not just the draft (a stored non-auto entry with a draft flipped
  // to auto would otherwise show a button that can only 400).
  stubFetch({ '/api/products/': unmatchedGameProduct })
  renderPanel(entryFixture({ pricing_mode: 'proxy' }), { mode: 'auto', productId: undefined, customValue: '' })
  expect(await screen.findByText(/no confirmed price listing yet/i)).toBeInTheDocument()
  expect(screen.queryByRole('button', { name: 'Match listing' })).not.toBeInTheDocument()
  cleanup()

  // auto + matched game -> absent (already has a confirmed listing)
  stubFetch({ '/api/products/': matchedProduct })
  renderPanel(entryFixture({ pricing_mode: 'auto' }))
  expect(await screen.findByText(/match 93%/i)).toBeInTheDocument()
  expect(screen.queryByRole('button', { name: 'Match listing' })).not.toBeInTheDocument()
  cleanup()

  // auto + unmatched product missing igdb/platform -> absent (identity incomplete)
  stubFetch({ '/api/products/': { ...unmatchedGameProduct, igdb: undefined, platform: undefined } })
  renderPanel(entryFixture({ pricing_mode: 'auto' }))
  expect(await screen.findByText(/no confirmed price listing yet/i)).toBeInTheDocument()
  expect(screen.queryByRole('button', { name: 'Match listing' })).not.toBeInTheDocument()
  cleanup()

  // proxy mode -> absent (the auto block never renders outside auto)
  stubFetch({ '/api/products/': unmatchedGameProduct })
  renderPanel(entryFixture({ pricing_mode: 'proxy', pricing_product_id: 'p9' }))
  expect(screen.queryByRole('button', { name: 'Match listing' })).not.toBeInTheDocument()
  cleanup()

  // custom entry (no product_id) -> absent, even with mode forced to auto in the draft
  stubFetch({})
  renderPanel(
    entryFixture({ product_id: undefined, pricing_mode: 'disabled', pricing_product_id: undefined }),
    { mode: 'auto', productId: undefined, customValue: '' },
  )
  expect(screen.queryByRole('button', { name: 'Match listing' })).not.toBeInTheDocument()
})

it('resolves the picked listing and PUTs the entry onto the member', async () => {
  const entry = entryFixture({ pricing_mode: 'auto' })
  const member = {
    id: 'm7', type: 'game', name: 'Chrono Trigger',
    pricecharting: {
      pc_product_id: 7042, pc_name: 'Chrono Trigger [PAL]', console_name: 'PAL Super Nintendo',
      match_confidence: 1.0, verified: false, as_of: 'x',
    },
    created_at: 'x', updated_at: 'x',
  }
  const updatedEntry = { ...entry, product_id: 'm7' }
  const fetchMock = stubFetch({
    // More specific prefixes first: stubFetch matches by URL prefix, and
    // '/api/products/resolve' also starts with '/api/products/'.
    '/api/products/resolve': member,
    '/api/search': {
      degraded: false,
      results: [{
        type: 'pc_listing', name: 'Chrono Trigger [PAL]', pc_product_id: 7042,
        console_name: 'PAL Super Nintendo', loose_cents: 9800,
      }],
    },
    '/api/products/': unmatchedGameProduct,
    [`/api/entries/${entry.id}`]: updatedEntry,
  })
  renderPanel(entry)
  await userEvent.click(await screen.findByRole('button', { name: 'Match listing' }))
  expect(await screen.findByRole('dialog', { name: 'Match a price listing' })).toBeInTheDocument()
  await userEvent.click(await screen.findByRole('button', { name: /use chrono trigger/i }))

  await waitFor(() => {
    expect(
      fetchMock.mock.calls.some(
        (c) => c[0] === `/api/entries/${entry.id}` && (c[1] as RequestInit | undefined)?.method === 'PUT',
      ),
    ).toBe(true)
  })

  const resolveCall = fetchMock.mock.calls.find((c) => c[0] === '/api/products/resolve')
  expect(putBody(resolveCall?.[1] as RequestInit)).toEqual({
    type: 'game', igdb_game_id: 1000, platform_igdb_id: 6, pc_product_id: 7042,
  })

  const putCall = fetchMock.mock.calls.find(
    (c) => c[0] === `/api/entries/${entry.id}` && (c[1] as RequestInit | undefined)?.method === 'PUT',
  )
  const body = putBody<{ product_id?: string; pricing_mode?: string }>(putCall?.[1] as RequestInit)
  expect(body.product_id).toBe('m7')
  expect(body.pricing_mode).toBe('auto')
})

it('surfaces a failed match', async () => {
  const entry = entryFixture({ pricing_mode: 'auto' })
  const member = { id: 'm7', type: 'game', name: 'Chrono Trigger', created_at: 'x', updated_at: 'x' }
  const fetchMock = vi.fn().mockImplementation((url: string, init?: RequestInit) => {
    const u = String(url)
    if (u === '/api/products/resolve') return Promise.resolve(jsonResponse(200, member))
    if (u.startsWith('/api/search')) {
      return Promise.resolve(jsonResponse(200, {
        degraded: false,
        results: [{
          type: 'pc_listing', name: 'Chrono Trigger [PAL]', pc_product_id: 7042,
          console_name: 'PAL Super Nintendo', loose_cents: 9800,
        }],
      }))
    }
    if (u === `/api/entries/${entry.id}` && init?.method === 'PUT') {
      return Promise.resolve(jsonResponse(400, { code: 'invalid_product_change', detail: 'not eligible' }))
    }
    if (u.startsWith('/api/products/')) return Promise.resolve(jsonResponse(200, unmatchedGameProduct))
    return Promise.resolve(jsonResponse(404, {}))
  })
  vi.stubGlobal('fetch', fetchMock)
  renderPanel(entry)
  await userEvent.click(await screen.findByRole('button', { name: 'Match listing' }))
  expect(await screen.findByRole('dialog', { name: 'Match a price listing' })).toBeInTheDocument()
  await userEvent.click(await screen.findByRole('button', { name: /use chrono trigger/i }))
  expect(await screen.findByRole('alert')).toHaveTextContent(
    'The listing match failed; try again in a moment.',
  )
})
