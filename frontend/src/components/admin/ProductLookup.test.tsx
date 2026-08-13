import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { jsonResponse, problemResponse } from '../../test/fixtures'
import { renderWithI18n } from '../../test/i18n'
import ProductLookup from './ProductLookup'

function renderLookup() {
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } })
  renderWithI18n(
    <QueryClientProvider client={qc}>
      <ProductLookup />
    </QueryClientProvider>,
  )
}

afterEach(() => vi.unstubAllGlobals())

it('fetches a product by id and shows its mapping state with fix actions', async () => {
  const user = userEvent.setup()
  vi.stubGlobal('fetch', vi.fn().mockResolvedValue(jsonResponse(200, {
    id: '4242', type: 'game', name: 'Chrono Trigger',
    platform: { igdb_platform_id: 6, name: 'SNES' },
    pricecharting: {
      pc_product_id: 9010, pc_name: 'Chrono Trigger', console_name: 'Super Nintendo',
      match_confidence: 0.9, verified: false, as_of: '2026-07-01T00:00:00Z',
    },
    created_at: '2026-07-01T00:00:00Z', updated_at: '2026-07-01T00:00:00Z',
  })))
  renderLookup()
  await user.type(screen.getByRole('textbox', { name: 'Product id' }), '4242')
  await user.click(screen.getByRole('button', { name: 'Look up' }))
  expect(await screen.findByText('Chrono Trigger')).toBeInTheDocument()
  expect(screen.getByLabelText('Fix mapping for Chrono Trigger')).toBeInTheDocument()
  expect(screen.getByText(/match 90%/i)).toBeInTheDocument()
})

it('shows a pending line while the lookup is in flight', async () => {
  const user = userEvent.setup()
  vi.stubGlobal('fetch', vi.fn().mockImplementation(() => new Promise(() => {})))
  renderLookup()
  await user.type(screen.getByRole('textbox', { name: 'Product id' }), '4242')
  await user.click(screen.getByRole('button', { name: 'Look up' }))
  expect(await screen.findByText('Looking up...')).toBeInTheDocument()
})

it('re-running the same id refetches the product', async () => {
  const user = userEvent.setup()
  const fetchMock = vi.fn().mockResolvedValue(jsonResponse(200, {
    id: '4242', type: 'game', name: 'Chrono Trigger',
    platform: { igdb_platform_id: 6, name: 'SNES' },
    created_at: '2026-07-01T00:00:00Z', updated_at: '2026-07-01T00:00:00Z',
  }))
  vi.stubGlobal('fetch', fetchMock)
  renderLookup()
  await user.type(screen.getByRole('textbox', { name: 'Product id' }), '4242')
  await user.click(screen.getByRole('button', { name: 'Look up' }))
  expect(await screen.findByText('Chrono Trigger')).toBeInTheDocument()
  await user.click(screen.getByRole('button', { name: 'Look up' }))
  await waitFor(() => expect(fetchMock).toHaveBeenCalledTimes(2))
})

it('renders a plain message when the id is unknown', async () => {
  const user = userEvent.setup()
  vi.stubGlobal('fetch', vi.fn().mockResolvedValue(problemResponse(404, 'product_not_found', 'no such product')))
  renderLookup()
  await user.type(screen.getByRole('textbox', { name: 'Product id' }), 'missing-id')
  await user.click(screen.getByRole('button', { name: 'Look up' }))
  expect(await screen.findByRole('alert')).toHaveTextContent('No product with that id.')
})

it('resets the promote panel state when the lookup target changes', async () => {
  const user = userEvent.setup()
  const communityGame = (id: string, name: string) => ({
    id, type: 'game', name, origin: 'community',
    community: { platform_name: 'SNES' },
    created_at: '2026-07-01T00:00:00Z', updated_at: '2026-07-01T00:00:00Z',
  })
  const candidatesPage = (id: string, name: string, candName: string) => ({
    products: [{
      product: communityGame(id, name),
      candidates: [{ provider: 'igdb', provider_id: 1011, name: candName, score: 0.92, found_at: '2026-07-01T00:00:00Z' }],
    }],
    total_count: 1,
  })
  vi.stubGlobal('fetch', vi.fn().mockImplementation((url: string) => {
    const u = String(url)
    if (u.startsWith('/api/search')) {
      // Only the listing-attach picker (pc_listing) surfaces the listing;
      // the game picker's own auto-fired search stays empty so the "Use"
      // button is unambiguous.
      if (u.includes('type=pc_listing'))
        return Promise.resolve(jsonResponse(200, {
          degraded: false,
          results: [{ type: 'pc_listing', name: 'Chrono Listing', pc_product_id: 7788, console_name: 'Super Nintendo' }],
        }))
      return Promise.resolve(jsonResponse(200, { degraded: false, results: [] }))
    }
    if (u.startsWith('/api/admin/products/promote-candidates'))
      return Promise.resolve(jsonResponse(200, u.includes('product_id=pB')
        ? candidatesPage('pB', 'Repro Beta', 'Secret of Mana')
        : candidatesPage('pA', 'Repro Alpha', 'Chrono Trigger')))
    if (u === '/api/products/pB') return Promise.resolve(jsonResponse(200, communityGame('pB', 'Repro Beta')))
    return Promise.resolve(jsonResponse(200, communityGame('pA', 'Repro Alpha')))
  }))
  const lookUp = async (id: string) => {
    const input = screen.getByRole('textbox', { name: 'Product id' })
    await user.clear(input)
    await user.type(input, id)
    await user.click(screen.getByRole('button', { name: 'Look up' }))
  }
  renderLookup()
  // Warm pB into the query cache first: switching to a CACHED product
  // keeps the panel mounted (isSuccess never flips false), which is the
  // path that leaks state; a never-seen product reloads and remounts on
  // its own.
  await lookUp('pB')
  expect(await screen.findByText('Repro Beta')).toBeInTheDocument()
  await lookUp('pA')
  expect(await screen.findByText('Repro Alpha')).toBeInTheDocument()
  // Open the picker and attach a listing (the pc_listing search seeds
  // from the candidate name and auto-fires).
  await user.click(screen.getByRole('button', { name: 'Promote to provider identity' }))
  await user.click(screen.getByRole('button', { name: /attach a price listing/i }))
  await user.click(await screen.findByRole('button', { name: 'Use Chrono Listing' }))
  expect(await screen.findByText('Listing: Chrono Listing')).toBeInTheDocument()
  // Switch to the cached community product: no attached-listing residue.
  await lookUp('pB')
  expect(await screen.findByText('Repro Beta')).toBeInTheDocument()
  expect(screen.queryByText('Listing: Chrono Listing')).not.toBeInTheDocument()
})

it('shows the community badge and the promote panel for a community product', async () => {
  const user = userEvent.setup()
  const fetchMock = vi.fn().mockImplementation((url: string) => {
    if (url.startsWith('/api/admin/products/promote-candidates'))
      return Promise.resolve(jsonResponse(200, {
        products: [{
          product: {
            id: 'p9', type: 'game', name: 'Repro Alpha', origin: 'community',
            community: { platform_name: 'SNES' },
            created_at: '2026-07-01T00:00:00Z', updated_at: '2026-07-01T00:00:00Z',
          },
          candidates: [
            { provider: 'igdb', provider_id: 1011, name: 'Chrono Trigger', score: 0.92, found_at: '2026-07-01T00:00:00Z' },
          ],
        }],
        total_count: 1,
      }))
    return Promise.resolve(jsonResponse(200, {
      id: 'p9', type: 'game', name: 'Repro Alpha', origin: 'community',
      community: { platform_name: 'SNES' },
      created_at: '2026-07-01T00:00:00Z', updated_at: '2026-07-01T00:00:00Z',
    }))
  })
  vi.stubGlobal('fetch', fetchMock)
  renderLookup()
  await user.type(screen.getByRole('textbox', { name: 'Product id' }), 'p9')
  await user.click(screen.getByRole('button', { name: 'Look up' }))
  expect(await screen.findByText('Repro Alpha')).toBeInTheDocument()
  expect(screen.getByText('community')).toBeInTheDocument()
  expect(screen.getByLabelText('Promote Repro Alpha')).toBeInTheDocument()
  expect(screen.queryByLabelText('Fix mapping for Repro Alpha')).not.toBeInTheDocument()
})

it('shows a region line for a community product carrying one', async () => {
  const user = userEvent.setup()
  vi.stubGlobal('fetch', vi.fn().mockResolvedValue(jsonResponse(200, {
    id: 'p9', type: 'game', name: 'Repro Alpha', origin: 'community',
    community: { platform_name: 'SNES', region: 'ntsc_j' },
    created_at: '2026-07-01T00:00:00Z', updated_at: '2026-07-01T00:00:00Z',
  })))
  renderLookup()
  await user.type(screen.getByRole('textbox', { name: 'Product id' }), 'p9')
  await user.click(screen.getByRole('button', { name: 'Look up' }))
  expect(await screen.findByText('Repro Alpha')).toBeInTheDocument()
  expect(screen.getByText('Region: NTSC-J')).toBeInTheDocument()
})

it('shows no region line for a community product carrying none', async () => {
  const user = userEvent.setup()
  vi.stubGlobal('fetch', vi.fn().mockResolvedValue(jsonResponse(200, {
    id: 'p9', type: 'game', name: 'Repro Alpha', origin: 'community',
    community: { platform_name: 'SNES' },
    created_at: '2026-07-01T00:00:00Z', updated_at: '2026-07-01T00:00:00Z',
  })))
  renderLookup()
  await user.type(screen.getByRole('textbox', { name: 'Product id' }), 'p9')
  await user.click(screen.getByRole('button', { name: 'Look up' }))
  expect(await screen.findByText('Repro Alpha')).toBeInTheDocument()
  expect(screen.queryByText(/^Region:/)).not.toBeInTheDocument()
})
