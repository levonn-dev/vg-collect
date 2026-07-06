import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { render, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { entryFixture, jsonResponse } from '../../test/fixtures'
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

function stubFetch(handlers: Record<string, unknown>, onPut?: (body: unknown) => Response) {
  const fetchMock = vi.fn().mockImplementation((url: string, init?: RequestInit) => {
    if (init?.method === 'PUT' && onPut) {
      return Promise.resolve(onPut(JSON.parse(init.body as string)))
    }
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

function renderPanel(entry = entryFixture(), qc = new QueryClient({ defaultOptions: { queries: { retry: false } } })) {
  render(
    <QueryClientProvider client={qc}>
      <PricingPanel entry={entry} />
    </QueryClientProvider>,
  )
  return qc
}

afterEach(() => vi.unstubAllGlobals())

it('renders the absent value neutrally, without guessing a cause', () => {
  stubFetch({ '/api/products/': matchedProduct })
  renderPanel(entryFixture({ value_cents: undefined }))
  expect(screen.getByText('No market value available.')).toBeInTheDocument()
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

it('switching to disabled keeps the parked proxy id (memory, not erasure)', async () => {
  let putBody: Record<string, unknown> = {}
  const e = entryFixture({ pricing_mode: 'proxy', pricing_product_id: 'p9' })
  stubFetch({ '/api/products/': matchedProduct }, (body) => {
    putBody = body as Record<string, unknown>
    return jsonResponse(200, { ...e, pricing_mode: 'disabled' })
  })
  renderPanel(e)
  await userEvent.click(screen.getByRole('radio', { name: /disabled/i }))
  expect(putBody.pricing_mode).toBe('disabled')
  expect(putBody.pricing_product_id).toBe('p9')
})

it('invalidates the recommendations cache after a pricing change', async () => {
  // On custom entries the server re-snapshots or clears the entry's
  // recommendation identity when the pricing proxy changes, so a
  // pricing mutation alters the library summary feeding recommendations.
  const e = entryFixture({ pricing_mode: 'proxy', pricing_product_id: 'p9' })
  stubFetch({ '/api/products/': matchedProduct }, () =>
    jsonResponse(200, { ...e, pricing_mode: 'disabled' }))
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } })
  qc.setQueryData(['recommendations'], { stale: true })
  renderPanel(e, qc)
  await userEvent.click(screen.getByRole('radio', { name: /disabled/i }))
  await waitFor(() => expect(qc.getQueryState(['recommendations'])?.isInvalidated).toBe(true))
})

it('presents a parked target as memory with a reactivate affordance', async () => {
  const e = entryFixture({ pricing_mode: 'disabled', pricing_product_id: 'p9', product_id: undefined })
  let putBody: Record<string, unknown> = {}
  stubFetch({ '/api/products/': matchedProduct }, (body) => {
    putBody = body as Record<string, unknown>
    return jsonResponse(200, { ...e, pricing_mode: 'proxy' })
  })
  renderPanel(e)
  expect(await screen.findByText(/last price proxy/i)).toBeInTheDocument()
  await userEvent.click(screen.getByRole('button', { name: /reactivate/i }))
  expect(putBody.pricing_mode).toBe('proxy')
  expect(putBody.pricing_product_id).toBe('p9')
})

it('surfaces a 404 when a reactivated target no longer exists', async () => {
  const e = entryFixture({ pricing_mode: 'disabled', pricing_product_id: 'p9' })
  stubFetch({ '/api/products/': matchedProduct }, () =>
    jsonResponse(404, {
      type: 'about:blank', title: 'Not Found', status: 404,
      code: 'unknown_pricing_product', detail: 'no such pricing product in the catalog',
    }))
  renderPanel(e)
  await userEvent.click(await screen.findByRole('button', { name: /reactivate/i }))
  expect(await screen.findByRole('alert')).toHaveTextContent(/no such pricing product/i)
})

it('offers auto only to product-backed entries and opens the picker for a first proxy', async () => {
  stubFetch({ '/api/products/': matchedProduct })
  renderPanel(entryFixture({ product_id: undefined, pricing_mode: 'disabled', pricing_product_id: undefined }))
  expect(screen.queryByRole('radio', { name: /^auto/i })).not.toBeInTheDocument()
  await userEvent.click(screen.getByRole('radio', { name: /proxy/i }))
  expect(await screen.findByRole('dialog', { name: /choose a price source/i })).toBeInTheDocument()
})

it('drives a fresh proxy pick through resolve and PUTs the resolved target', async () => {
  const resolved = { id: 'p7', type: 'game', name: 'Chrono Trigger', created_at: 'x', updated_at: 'x' }
  const e = entryFixture({ pricing_mode: 'disabled', pricing_product_id: undefined })
  let putBody: Record<string, unknown> = {}
  stubFetch({
    '/api/search': {
      degraded: false,
      results: [{ type: 'game', name: 'Chrono Trigger', igdb_game_id: 1000, platforms: [{ igdb_platform_id: 6, name: 'SNES' }] }],
    },
    '/api/products/resolve': resolved,
  }, (body) => {
    putBody = body as Record<string, unknown>
    return jsonResponse(200, { ...e, pricing_mode: 'proxy', pricing_product_id: resolved.id })
  })
  renderPanel(e)
  await userEvent.click(screen.getByRole('radio', { name: /proxy/i }))
  await userEvent.type(await screen.findByRole('searchbox', { name: /search/i }), 'chrono')
  await userEvent.click(screen.getByRole('button', { name: 'Search' }))
  await userEvent.click(await screen.findByRole('button', { name: 'Chrono Trigger on SNES' }))
  await waitFor(() => expect(putBody.pricing_mode).toBe('proxy'))
  expect(putBody.pricing_product_id).toBe('p7')
})
