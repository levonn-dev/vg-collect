import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { fireEvent, screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { MemoryRouter, Route, Routes } from 'react-router'
import { entryFixture, fxRatesFixture, jsonResponse, meFixture, putBody, requestPath } from '../test/fixtures'
import { renderWithI18n } from '../test/i18n'
import AddWizard from './AddWizard'

const searchAnswer = {
  degraded: false,
  results: [{
    type: 'game', name: 'Chrono Trigger', igdb_game_id: 1000,
    platforms: [{ igdb_platform_id: 6, name: 'SNES' }],
  }],
}
// Same shape as searchAnswer, but this result carries a matched_region
// and a matching localizations bundle, so its game pick suggests a
// wizard region and the heading derives from the bundle.
const jpMatchedSearchAnswer = {
  degraded: false,
  results: [{
    type: 'game', name: 'Chrono Trigger', igdb_game_id: 1000,
    matched_region: 'ja-JP',
    localizations: [{ region: 'ja-JP', name: '聖剣伝説', translit: 'Seiken Densetsu' }],
    platforms: [{ igdb_platform_id: 6, name: 'SNES' }],
  }],
}
const product = {
  id: 'p1', type: 'game', name: 'Chrono Trigger',
  platform: { igdb_platform_id: 6, name: 'SNES' },
  pricecharting: {
    pc_product_id: 55, pc_name: 'Chrono Trigger', console_name: 'Super Nintendo',
    match_confidence: 0.93, verified: false, loose_cents: 1500, as_of: '2026-07-01T00:00:00Z',
  },
  created_at: '2026-07-01T00:00:00Z', updated_at: '2026-07-01T00:00:00Z',
}

function renderWizard(
  path = '/add',
  qc = new QueryClient({ defaultOptions: { queries: { retry: false, staleTime: Infinity } } }),
  currency = 'USD',
  rates = true,
) {
  // SearchPicker now reads useDisplayMoney unconditionally; seed its
  // queries so they never hit the fetch mock (whose single mocked
  // Response the app-level assertions in these tests already read once).
  // rates: false skips the ['fx'] seed: the query settles from the
  // fetch mock's unmatched-URL fallback, so useDisplayMoney's currency
  // falls back to USD while profileCurrency keeps the given value -
  // the same rates-unavailable convention as renderWithMoney.
  qc.setQueryData(['me'], meFixture({ preferred_currency: currency }))
  if (rates) qc.setQueryData(['fx'], fxRatesFixture())
  return {
    qc,
    ...renderWithI18n(
      <QueryClientProvider client={qc}>
        <MemoryRouter initialEntries={[path]}>
          <Routes>
            <Route path="/add" element={<AddWizard />} />
            <Route path="/entries/:id" element={<div>entry-detail</div>} />
          </Routes>
        </MemoryRouter>
      </QueryClientProvider>,
    ),
  }
}

afterEach(() => vi.unstubAllGlobals())

it('walks search, details, and match confirmation to a created entry', async () => {
  const created = entryFixture({ display_name: 'Chrono Trigger' })
  const fetchMock = vi.fn().mockImplementation((url: unknown) => {
    const u = requestPath(url)
    if (u.startsWith('/api/search')) return Promise.resolve(jsonResponse(200, searchAnswer))
    if (u === '/api/products/resolve') return Promise.resolve(jsonResponse(200, product))
    if (u === '/api/entries' && (url as Request).method === 'POST') return Promise.resolve(jsonResponse(201, created))
    return Promise.resolve(jsonResponse(404, {}))
  })
  vi.stubGlobal('fetch', fetchMock)
  renderWizard()

  await userEvent.type(screen.getByRole('searchbox', { name: /search/i }), 'chrono')
  await userEvent.click(screen.getByRole('button', { name: 'Search' }))
  await userEvent.click(await screen.findByRole('button', { name: 'Chrono Trigger on SNES' }))

  expect(await screen.findByRole('heading', { name: /your copy of chrono trigger/i })).toBeInTheDocument()
  await userEvent.selectOptions(screen.getByLabelText('Status'), 'beaten')
  await userEvent.selectOptions(screen.getByLabelText('Rating'), '9')
  await userEvent.click(screen.getByRole('button', { name: 'Continue' }))

  // Match confidence surfaces before commitment.
  expect(await screen.findByText(/match 93%/i)).toBeInTheDocument()
  await userEvent.click(screen.getByRole('button', { name: 'Add to collection' }))
  expect(await screen.findByText('entry-detail')).toBeInTheDocument()

  const post = fetchMock.mock.calls.find((c) => (c[0] as Request).method === 'POST' && requestPath(c[0]) === '/api/entries')
  const body = await putBody<Record<string, unknown>>(post?.[0])
  expect(body.product_id).toBe('p1')
  expect(body.status).toBe('beaten')
  expect(body.rating).toBe(9)
  expect(body.display_name).toBeUndefined() // catalog facts come from the product
})

it('seeds the details region select from a matched-region game pick', async () => {
  const fetchMock = vi.fn().mockImplementation((url: unknown) => {
    const u = requestPath(url)
    if (u.startsWith('/api/search')) return Promise.resolve(jsonResponse(200, jpMatchedSearchAnswer))
    return Promise.resolve(jsonResponse(404, {}))
  })
  vi.stubGlobal('fetch', fetchMock)
  renderWizard()

  await userEvent.type(screen.getByRole('searchbox', { name: /search/i }), 'chrono')
  await userEvent.click(screen.getByRole('button', { name: 'Search' }))
  await userEvent.click(await screen.findByRole('button', { name: 'Chrono Trigger on SNES' }))

  expect(await screen.findByRole('heading', { name: 'Your copy of Seiken Densetsu' })).toBeInTheDocument()
  expect(screen.getByLabelText('Region')).toHaveValue('ntsc_j')
})

it('defaults the details region select to ntsc_u when the pick carries no region suggestion', async () => {
  const fetchMock = vi.fn().mockImplementation((url: unknown) => {
    const u = requestPath(url)
    if (u.startsWith('/api/search')) return Promise.resolve(jsonResponse(200, searchAnswer))
    return Promise.resolve(jsonResponse(404, {}))
  })
  vi.stubGlobal('fetch', fetchMock)
  renderWizard()

  await userEvent.type(screen.getByRole('searchbox', { name: /search/i }), 'chrono')
  await userEvent.click(screen.getByRole('button', { name: 'Search' }))
  await userEvent.click(await screen.findByRole('button', { name: 'Chrono Trigger on SNES' }))

  expect(await screen.findByRole('heading', { name: /your copy of chrono trigger/i })).toBeInTheDocument()
  expect(screen.getByLabelText('Region')).toHaveValue('ntsc_u')
})

it('seeds the details region select from a hardware pick', async () => {
  const hardwareSearchAnswer = {
    degraded: false,
    results: [{
      type: 'hardware', name: 'Super Famicom Console', pc_product_id: 6101,
      console_name: 'Super Famicom', category: 'Systems',
    }],
  }
  const fetchMock = vi.fn().mockImplementation((url: unknown) => {
    const u = requestPath(url)
    if (u.startsWith('/api/search')) return Promise.resolve(jsonResponse(200, hardwareSearchAnswer))
    return Promise.resolve(jsonResponse(404, {}))
  })
  vi.stubGlobal('fetch', fetchMock)
  renderWizard()

  await userEvent.click(screen.getByRole('radio', { name: /hardware/i }))
  await userEvent.type(screen.getByRole('searchbox', { name: /search/i }), 'famicom')
  await userEvent.click(screen.getByRole('button', { name: 'Search' }))
  await userEvent.click(await screen.findByRole('button', { name: 'Add Super Famicom Console' }))

  expect(await screen.findByRole('heading', { name: /your copy of super famicom console/i })).toBeInTheDocument()
  expect(screen.getByLabelText('Region')).toHaveValue('ntsc_j')
})

it('seeds the details region select from a community pick with region', async () => {
  const communityRegionSearchAnswer = {
    degraded: false,
    results: [{
      type: 'game', name: 'Repro Alpha', origin: 'community',
      product_id: 'c0ffee00-0000-4000-8000-000000000001', item_type: 'game',
      platform_name: 'SNES', region: 'pal',
    }],
  }
  const fetchMock = vi.fn().mockImplementation((url: unknown) => {
    const u = requestPath(url)
    if (u.startsWith('/api/search')) return Promise.resolve(jsonResponse(200, communityRegionSearchAnswer))
    return Promise.resolve(jsonResponse(404, {}))
  })
  vi.stubGlobal('fetch', fetchMock)
  renderWizard()

  await userEvent.type(screen.getByRole('searchbox', { name: /search/i }), 'repro')
  await userEvent.click(screen.getByRole('button', { name: 'Search' }))
  await userEvent.click(await screen.findByRole('button', { name: 'Repro Alpha on SNES' }))

  expect(await screen.findByRole('heading', { name: /your copy of repro alpha/i })).toBeInTheDocument()
  expect(screen.getByLabelText('Region')).toHaveValue('pal')
})

it('stamps the created entry with the profile currency', async () => {
  const created = entryFixture({ display_name: 'Chrono Trigger', currency: 'EUR' })
  const fetchMock = vi.fn().mockImplementation((url: unknown) => {
    const u = requestPath(url)
    if (u.startsWith('/api/search')) return Promise.resolve(jsonResponse(200, searchAnswer))
    if (u === '/api/products/resolve') return Promise.resolve(jsonResponse(200, product))
    if (u === '/api/entries' && (url as Request).method === 'POST') return Promise.resolve(jsonResponse(201, created))
    return Promise.resolve(jsonResponse(404, {}))
  })
  vi.stubGlobal('fetch', fetchMock)
  // Rates down: money.currency falls back to USD while profileCurrency
  // stays EUR, so this only passes if the create call stamps
  // profileCurrency - with rates up the two values are identical and
  // the assertion below could not tell them apart.
  renderWizard('/add', undefined, 'EUR', false)

  await userEvent.type(screen.getByRole('searchbox', { name: /search/i }), 'chrono')
  await userEvent.click(screen.getByRole('button', { name: 'Search' }))
  await userEvent.click(await screen.findByRole('button', { name: 'Chrono Trigger on SNES' }))
  expect(await screen.findByRole('heading', { name: /your copy of chrono trigger/i })).toBeInTheDocument()
  await userEvent.click(screen.getByRole('button', { name: 'Continue' }))
  expect(await screen.findByText(/match 93%/i)).toBeInTheDocument()
  await userEvent.click(screen.getByRole('button', { name: 'Add to collection' }))
  expect(await screen.findByText('entry-detail')).toBeInTheDocument()

  const post = fetchMock.mock.calls.find((c) => (c[0] as Request).method === 'POST' && requestPath(c[0]) === '/api/entries')
  const body = await putBody<Record<string, unknown>>(post?.[0])
  expect(body.currency).toBe('EUR')
})

it('invalidates the dashboard and recommendations caches on create', async () => {
  const created = entryFixture({ display_name: 'Chrono Trigger' })
  const fetchMock = vi.fn().mockImplementation((url: unknown) => {
    const u = requestPath(url)
    if (u.startsWith('/api/search')) return Promise.resolve(jsonResponse(200, searchAnswer))
    if (u === '/api/products/resolve') return Promise.resolve(jsonResponse(200, product))
    if (u === '/api/entries' && (url as Request).method === 'POST') return Promise.resolve(jsonResponse(201, created))
    return Promise.resolve(jsonResponse(404, {}))
  })
  vi.stubGlobal('fetch', fetchMock)
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false, staleTime: Infinity } } })
  qc.setQueryData(['dashboard'], { stale: true })
  qc.setQueryData(['recommendations'], { stale: true })
  renderWizard('/add', qc)

  await userEvent.type(screen.getByRole('searchbox', { name: /search/i }), 'chrono')
  await userEvent.click(screen.getByRole('button', { name: 'Search' }))
  await userEvent.click(await screen.findByRole('button', { name: 'Chrono Trigger on SNES' }))
  expect(await screen.findByRole('heading', { name: /your copy of chrono trigger/i })).toBeInTheDocument()
  await userEvent.click(screen.getByRole('button', { name: 'Continue' }))
  expect(await screen.findByText(/match 93%/i)).toBeInTheDocument()
  await userEvent.click(screen.getByRole('button', { name: 'Add to collection' }))
  expect(await screen.findByText('entry-detail')).toBeInTheDocument()

  expect(qc.getQueryState(['dashboard'])?.isInvalidated).toBe(true)
  expect(qc.getQueryState(['recommendations'])?.isInvalidated).toBe(true)
})

it('keeps typed details across a Confirm Back, and each Back returns to the previous step', async () => {
  const fetchMock = vi.fn().mockImplementation((url: unknown) => {
    const u = requestPath(url)
    if (u.startsWith('/api/search')) return Promise.resolve(jsonResponse(200, searchAnswer))
    if (u === '/api/products/resolve') return Promise.resolve(jsonResponse(200, product))
    return Promise.resolve(jsonResponse(404, {}))
  })
  vi.stubGlobal('fetch', fetchMock)
  renderWizard()

  await userEvent.type(screen.getByRole('searchbox', { name: /search/i }), 'chrono')
  await userEvent.click(screen.getByRole('button', { name: 'Search' }))
  await userEvent.click(await screen.findByRole('button', { name: 'Chrono Trigger on SNES' }))

  expect(await screen.findByRole('heading', { name: /your copy of chrono trigger/i })).toBeInTheDocument()
  await userEvent.type(screen.getByLabelText(/edition or variant/i), 'first print')
  await userEvent.type(screen.getByLabelText(/price paid/i), '42.50')
  await userEvent.selectOptions(screen.getByLabelText('Status'), 'beaten')
  await userEvent.click(screen.getByRole('button', { name: 'Continue' }))

  // Confirm renders; its Back returns to Details with every typed
  // field retained (the resolve-error copy on this step promises as
  // much, but the retention holds on the success path too).
  expect(await screen.findByText(/match 93%/i)).toBeInTheDocument()
  await userEvent.click(screen.getByRole('button', { name: 'Back' }))
  expect(await screen.findByLabelText(/edition or variant/i)).toHaveValue('first print')
  expect(screen.getByLabelText(/price paid/i)).toHaveValue('42.50')
  expect(screen.getByLabelText('Status')).toHaveValue('beaten')

  // Details' own Back returns to the search step.
  await userEvent.click(screen.getByRole('button', { name: 'Back' }))
  expect(await screen.findByRole('searchbox', { name: /search/i })).toBeInTheDocument()
})

it('keeps a manual match across Continue and Back', async () => {
  const listingAnswer = {
    degraded: false,
    results: [{ type: 'pc_listing', name: 'Chrono Trigger [PAL]', pc_product_id: 7042, console_name: 'PAL Super Nintendo', loose_cents: 9800 }],
  }
  const fetchMock = vi.fn().mockImplementation((url: unknown) => {
    const u = requestPath(url)
    if (u.startsWith('/api/search')) {
      return Promise.resolve(jsonResponse(200, u.includes('type=pc_listing') ? listingAnswer : searchAnswer))
    }
    if (u === '/api/products/resolve') return Promise.resolve(jsonResponse(200, product))
    return Promise.resolve(jsonResponse(404, {}))
  })
  vi.stubGlobal('fetch', fetchMock)
  renderWizard()

  await userEvent.type(screen.getByRole('searchbox', { name: /search for games and hardware/i }), 'chrono')
  await userEvent.click(screen.getByRole('button', { name: 'Search' }))
  await userEvent.click(await screen.findByRole('button', { name: 'Chrono Trigger on SNES' }))

  expect(await screen.findByRole('heading', { name: /your copy of chrono trigger/i })).toBeInTheDocument()
  await userEvent.click(screen.getByRole('button', { name: 'Match manually' }))
  await userEvent.click(await screen.findByRole('button', { name: /use chrono trigger \[pal\]/i }))
  expect(screen.getByText('Chrono Trigger [PAL]')).toBeInTheDocument()
  await userEvent.click(screen.getByRole('button', { name: 'Continue' }))

  // Confirm renders; Back returns to Details with the chip intact,
  // exactly like the typed-details retention.
  expect(await screen.findByText(/match \d+%/i)).toBeInTheDocument()
  await userEvent.click(screen.getByRole('button', { name: 'Back' }))
  expect(await screen.findByText('Chrono Trigger [PAL]')).toBeInTheDocument()
})

it('shows the match-pending state for an unmatched product', async () => {
  vi.stubGlobal('fetch', vi.fn().mockImplementation((url: unknown) => {
    const u = requestPath(url)
    if (u.startsWith('/api/search')) return Promise.resolve(jsonResponse(200, searchAnswer))
    if (u === '/api/products/resolve') {
      return Promise.resolve(jsonResponse(200, { ...product, pricecharting: undefined }))
    }
    return Promise.resolve(jsonResponse(404, {}))
  }))
  renderWizard()
  await userEvent.type(screen.getByRole('searchbox', { name: /search/i }), 'chrono')
  await userEvent.click(screen.getByRole('button', { name: 'Search' }))
  await userEvent.click(await screen.findByRole('button', { name: 'Chrono Trigger on SNES' }))
  await userEvent.click(screen.getByRole('button', { name: 'Continue' }))
  expect(await screen.findByText(/no confirmed price listing yet/i)).toBeInTheDocument()
})

it('fills a missing match from the confirm step', async () => {
  const listingAnswer = {
    degraded: false,
    results: [{ type: 'pc_listing', name: 'Chrono Trigger [PAL]', pc_product_id: 7042, console_name: 'PAL Super Nintendo', loose_cents: 9800 }],
  }
  const unanchored = { ...product, pricecharting: undefined }
  const filled = {
    ...product,
    pricecharting: { ...product.pricecharting, pc_product_id: 7042, pc_name: 'Chrono Trigger [PAL]', match_confidence: 1.0 },
  }
  const fetchMock = vi.fn().mockImplementation(async (url: unknown) => {
    const u = requestPath(url)
    if (u.startsWith('/api/search')) {
      return jsonResponse(200, u.includes('type=pc_listing') ? listingAnswer : searchAnswer)
    }
    if (u === '/api/products/resolve') {
      const body = await putBody<{ pc_product_id?: number }>(url)
      return jsonResponse(200, body.pc_product_id ? filled : unanchored)
    }
    return jsonResponse(404, {})
  })
  vi.stubGlobal('fetch', fetchMock)
  renderWizard()

  await userEvent.type(screen.getByRole('searchbox', { name: /search for games and hardware/i }), 'chrono')
  await userEvent.click(screen.getByRole('button', { name: 'Search' }))
  await userEvent.click(await screen.findByRole('button', { name: 'Chrono Trigger on SNES' }))
  expect(await screen.findByRole('heading', { name: /your copy of chrono trigger/i })).toBeInTheDocument()
  await userEvent.click(screen.getByRole('button', { name: 'Continue' }))

  // Auto-match missed; the card offers the remedy.
  expect(await screen.findByText(/no confirmed price listing yet/i)).toBeInTheDocument()
  await userEvent.click(screen.getByRole('button', { name: 'Match manually' }))
  await userEvent.click(await screen.findByRole('button', { name: /use chrono trigger \[pal\]/i }))

  // The re-resolve rides the choice and the card flips green.
  expect(await screen.findByText(/match 100%/i)).toBeInTheDocument()
  expect(screen.getByText(/priced as "chrono trigger \[pal\]"/i)).toBeInTheDocument()
})

it('pre-runs the q parameter (the recommendations add path)', async () => {
  const fetchMock = vi.fn().mockResolvedValue(jsonResponse(200, searchAnswer))
  vi.stubGlobal('fetch', fetchMock)
  renderWizard('/add?q=chrono')
  expect(await screen.findByText('Chrono Trigger')).toBeInTheDocument()
  expect(requestPath(fetchMock.mock.calls[0][0])).toContain('q=chrono')
})

it('creates a custom entry with pricing disabled', async () => {
  const created = entryFixture({ display_name: 'Chrono Trigger Repro', product_id: undefined, pricing_mode: 'disabled' })
  const fetchMock = vi.fn().mockImplementation((url: unknown) => {
    if (requestPath(url) === '/api/entries' && (url as Request).method === 'POST') {
      return Promise.resolve(jsonResponse(201, created))
    }
    return Promise.resolve(jsonResponse(404, {}))
  })
  vi.stubGlobal('fetch', fetchMock)
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false, staleTime: Infinity } } })
  qc.setQueryData(['platforms'], { platforms: [{ igdb_id: 19, name: 'Super Nintendo Entertainment System', aliases: ['snes'] }] })
  renderWizard('/add', qc)

  await userEvent.click(screen.getByRole('button', { name: /add it as a custom item/i }))
  await userEvent.type(screen.getByLabelText(/^name$/i), 'Chrono Trigger Repro')
  await userEvent.selectOptions(screen.getByLabelText(/item type/i), 'game')
  await userEvent.type(screen.getByLabelText(/platform/i), 'snes')
  await userEvent.click(await screen.findByRole('button', { name: 'Super Nintendo Entertainment System' }))
  await userEvent.click(screen.getByRole('button', { name: 'Add developer' }))
  await userEvent.type(screen.getByLabelText('Developers: 1'), '  Garage Team  ')
  await userEvent.click(screen.getByRole('button', { name: 'Add publisher' }))
  await userEvent.type(screen.getByLabelText('Publishers: 1'), 'Repro House')
  await userEvent.click(screen.getByRole('button', { name: 'Continue' }))

  await userEvent.click(await screen.findByRole('button', { name: 'Continue' })) // details step, defaults

  expect(await screen.findByText(/start without market pricing/i)).toBeInTheDocument()
  await userEvent.click(screen.getByRole('button', { name: 'Add to collection' }))
  expect(await screen.findByText('entry-detail')).toBeInTheDocument()

  const post = fetchMock.mock.calls.find((c) => (c[0] as Request).method === 'POST')
  const body = await putBody<Record<string, unknown>>(post?.[0])
  expect(body.product_id).toBeUndefined()
  expect(body.display_name).toBe('Chrono Trigger Repro')
  expect(body.item_type).toBe('game')
  expect(body.platform_name).toBe('Super Nintendo Entertainment System')
  expect(body.platform_igdb_id).toBe(19)
  expect(body.pricing_mode).toBe('disabled')
  // Credits ride trimmed; the wire body carries clean names.
  expect(body.developers).toEqual(['Garage Team'])
  expect(body.publishers).toEqual(['Repro House'])
  // The cover input was left empty: the wire body must carry no
  // cover_url key at all, not just an empty-string value.
  expect(body).not.toHaveProperty('cover_url')
})

it('shows the generic fallback message when a custom create fails with no recognized code', async () => {
  const fetchMock = vi.fn().mockImplementation((url: unknown) => {
    if (requestPath(url) === '/api/entries' && (url as Request).method === 'POST') {
      return Promise.reject(new TypeError('Failed to fetch'))
    }
    return Promise.resolve(jsonResponse(404, {}))
  })
  vi.stubGlobal('fetch', fetchMock)
  renderWizard()

  await userEvent.click(screen.getByRole('button', { name: /add it as a custom item/i }))
  await userEvent.type(screen.getByLabelText(/^name$/i), 'Chrono Trigger Repro')
  await userEvent.selectOptions(screen.getByLabelText(/item type/i), 'game')
  await userEvent.click(screen.getByRole('button', { name: 'Continue' }))
  await userEvent.click(await screen.findByRole('button', { name: 'Continue' })) // details step, defaults

  await userEvent.click(screen.getByRole('button', { name: 'Add to collection' }))
  expect(await screen.findByRole('alert')).toHaveTextContent('The entry could not be created.')
})

it('sends a cover url on a custom create when the wizard cover input is filled', async () => {
  const created = entryFixture({
    display_name: 'Chrono Trigger Repro', product_id: undefined, pricing_mode: 'disabled',
    cover_url: 'https://img.example/c.jpg',
  })
  const fetchMock = vi.fn().mockImplementation((url: unknown) => {
    if (requestPath(url) === '/api/entries' && (url as Request).method === 'POST') {
      return Promise.resolve(jsonResponse(201, created))
    }
    return Promise.resolve(jsonResponse(404, {}))
  })
  vi.stubGlobal('fetch', fetchMock)
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false, staleTime: Infinity } } })
  qc.setQueryData(['platforms'], { platforms: [{ igdb_id: 19, name: 'Super Nintendo Entertainment System', aliases: ['snes'] }] })
  renderWizard('/add', qc)

  await userEvent.click(screen.getByRole('button', { name: /add it as a custom item/i }))
  await userEvent.type(screen.getByLabelText(/^name$/i), 'Chrono Trigger Repro')
  await userEvent.selectOptions(screen.getByLabelText(/item type/i), 'game')
  await userEvent.type(screen.getByLabelText(/platform/i), 'snes')
  await userEvent.click(await screen.findByRole('button', { name: 'Super Nintendo Entertainment System' }))
  await userEvent.type(screen.getByLabelText(/cover image link/i), 'https://img.example/c.jpg')
  await userEvent.click(screen.getByRole('button', { name: 'Continue' }))

  await userEvent.click(await screen.findByRole('button', { name: 'Continue' })) // details step, defaults

  expect(await screen.findByText(/start without market pricing/i)).toBeInTheDocument()
  await userEvent.click(screen.getByRole('button', { name: 'Add to collection' }))
  expect(await screen.findByText('entry-detail')).toBeInTheDocument()

  const post = fetchMock.mock.calls.find((c) => (c[0] as Request).method === 'POST')
  const body = await putBody<Record<string, unknown>>(post?.[0])
  expect(body.cover_url).toBe('https://img.example/c.jpg')
  // Untouched credit lists send no keys at all.
  expect(body).not.toHaveProperty('developers')
  expect(body).not.toHaveProperty('publishers')
})

it('stamps a custom-created entry with the profile currency', async () => {
  const created = entryFixture({
    display_name: 'Chrono Trigger Repro', product_id: undefined, pricing_mode: 'disabled', currency: 'EUR',
  })
  const fetchMock = vi.fn().mockImplementation((url: unknown) => {
    if (requestPath(url) === '/api/entries' && (url as Request).method === 'POST') {
      return Promise.resolve(jsonResponse(201, created))
    }
    return Promise.resolve(jsonResponse(404, {}))
  })
  vi.stubGlobal('fetch', fetchMock)
  // Rates down: money.currency falls back to USD while profileCurrency
  // stays EUR, so this only passes if the create call stamps
  // profileCurrency - with rates up the two values are identical and
  // the assertion below could not tell them apart.
  renderWizard('/add', undefined, 'EUR', false)

  await userEvent.click(screen.getByRole('button', { name: /add it as a custom item/i }))
  await userEvent.type(screen.getByLabelText(/^name$/i), 'Chrono Trigger Repro')
  await userEvent.selectOptions(screen.getByLabelText(/item type/i), 'game')
  await userEvent.type(screen.getByLabelText(/platform/i), 'SNES')
  await userEvent.click(screen.getByRole('button', { name: 'Continue' }))
  await userEvent.click(await screen.findByRole('button', { name: 'Continue' })) // details step, defaults
  await userEvent.click(screen.getByRole('button', { name: 'Add to collection' }))
  expect(await screen.findByText('entry-detail')).toBeInTheDocument()

  const post = fetchMock.mock.calls.find((c) => (c[0] as Request).method === 'POST')
  const body = await putBody<Record<string, unknown>>(post?.[0])
  expect(body.currency).toBe('EUR')
})

it('invalidates the dashboard and recommendations caches on a custom create', async () => {
  const created = entryFixture({ display_name: 'Chrono Trigger Repro', product_id: undefined, pricing_mode: 'disabled' })
  const fetchMock = vi.fn().mockImplementation((url: unknown) => {
    if (requestPath(url) === '/api/entries' && (url as Request).method === 'POST') {
      return Promise.resolve(jsonResponse(201, created))
    }
    return Promise.resolve(jsonResponse(404, {}))
  })
  vi.stubGlobal('fetch', fetchMock)
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false, staleTime: Infinity } } })
  qc.setQueryData(['dashboard'], { stale: true })
  qc.setQueryData(['recommendations'], { stale: true })
  renderWizard('/add', qc)

  await userEvent.click(screen.getByRole('button', { name: /add it as a custom item/i }))
  await userEvent.type(screen.getByLabelText(/^name$/i), 'Chrono Trigger Repro')
  await userEvent.selectOptions(screen.getByLabelText(/item type/i), 'game')
  await userEvent.type(screen.getByLabelText(/platform/i), 'SNES')
  await userEvent.click(screen.getByRole('button', { name: 'Continue' }))
  await userEvent.click(await screen.findByRole('button', { name: 'Continue' })) // details step, defaults
  await userEvent.click(screen.getByRole('button', { name: 'Add to collection' }))
  expect(await screen.findByText('entry-detail')).toBeInTheDocument()

  expect(qc.getQueryState(['dashboard'])?.isInvalidated).toBe(true)
  expect(qc.getQueryState(['recommendations'])?.isInvalidated).toBe(true)
})

it('steers variants away from the custom path', async () => {
  vi.stubGlobal('fetch', vi.fn())
  renderWizard()
  await userEvent.click(screen.getByRole('button', { name: /add it as a custom item/i }))
  expect(screen.getByText(/variant of a searchable item/i)).toBeInTheDocument()
})

it('keeps typed values across both custom Back hops, in a full round trip', async () => {
  vi.stubGlobal('fetch', vi.fn())
  renderWizard()

  await userEvent.click(screen.getByRole('button', { name: /add it as a custom item/i }))
  await userEvent.type(screen.getByLabelText(/^name$/i), 'Chrono Trigger Repro')
  await userEvent.selectOptions(screen.getByLabelText(/item type/i), 'console')
  await userEvent.click(screen.getByRole('button', { name: /my platform isn't listed/i }))
  await userEvent.type(screen.getByLabelText(/platform/i), 'SNES')
  fireEvent.change(screen.getByLabelText(/release date/i), { target: { value: '1995-03-11' } })
  await userEvent.click(screen.getByRole('button', { name: 'Continue' }))

  expect(await screen.findByRole('heading', { name: /your copy of chrono trigger repro/i })).toBeInTheDocument()
  await userEvent.type(screen.getByLabelText(/edition or variant/i), 'first print')
  await userEvent.type(screen.getByLabelText(/price paid/i), '42.50')
  await userEvent.selectOptions(screen.getByLabelText('Status'), 'beaten')
  await userEvent.click(screen.getByRole('button', { name: 'Continue' }))

  expect(await screen.findByText(/start without market pricing/i)).toBeInTheDocument()

  // custom-confirm's Back must return to custom-details with everything retained.
  await userEvent.click(screen.getByRole('button', { name: 'Back' }))
  expect(await screen.findByLabelText(/edition or variant/i)).toHaveValue('first print')
  expect(screen.getByLabelText(/price paid/i)).toHaveValue('42.50')
  expect(screen.getByLabelText('Status')).toHaveValue('beaten')

  // custom-details' own Back must return to CustomStep with the identity retained.
  await userEvent.click(screen.getByRole('button', { name: 'Back' }))
  expect(await screen.findByLabelText(/^name$/i)).toHaveValue('Chrono Trigger Repro')
  expect(screen.getByLabelText(/item type/i)).toHaveValue('console')
  expect(screen.getByLabelText(/platform/i)).toHaveValue('SNES')
  expect(screen.getByLabelText(/release date/i)).toHaveValue('1995-03-11')

  // Forward again on both steps with nothing retyped: the whole round trip survives.
  await userEvent.click(screen.getByRole('button', { name: 'Continue' }))
  expect(await screen.findByLabelText(/edition or variant/i)).toHaveValue('first print')
  await userEvent.click(screen.getByRole('button', { name: 'Continue' }))
  expect(await screen.findByText(/confirm: chrono trigger repro/i)).toBeInTheDocument()
  expect(screen.getByText(/snes - console - custom item/i)).toBeInTheDocument()
})

it('adds a community pick straight from fetchProduct, never posting a resolve', async () => {
  const communityProduct = {
    id: 'c0ffee00-0000-4000-8000-000000000001', type: 'game', name: 'Repro Alpha',
    origin: 'community', community: { platform_name: 'SNES' },
    created_at: 'x', updated_at: 'x',
  }
  const communitySearchAnswer = {
    degraded: false,
    results: [
      {
        type: 'game', name: communityProduct.name, origin: 'community',
        product_id: communityProduct.id, item_type: 'game', platform_name: 'SNES',
      },
    ],
  }
  const created = entryFixture({ display_name: 'Repro Alpha' })
  const fetchMock = vi.fn().mockImplementation((url: unknown) => {
    const u = requestPath(url)
    if (u.startsWith('/api/search')) return Promise.resolve(jsonResponse(200, communitySearchAnswer))
    if (u === `/api/products/${communityProduct.id}`) return Promise.resolve(jsonResponse(200, communityProduct))
    if (u === '/api/entries' && (url as Request).method === 'POST') return Promise.resolve(jsonResponse(201, created))
    return Promise.resolve(jsonResponse(404, {}))
  })
  vi.stubGlobal('fetch', fetchMock)
  renderWizard()

  await userEvent.type(screen.getByRole('searchbox', { name: /search/i }), 'repro')
  await userEvent.click(screen.getByRole('button', { name: 'Search' }))
  await userEvent.click(await screen.findByRole('button', { name: 'Repro Alpha on SNES' }))

  expect(await screen.findByRole('heading', { name: /your copy of repro alpha/i })).toBeInTheDocument()
  await userEvent.click(screen.getByRole('button', { name: 'Continue' }))

  // The community product is already minted: confirm fetches it
  // directly, so no auto-match state ever shows for it.
  expect(await screen.findByText(/no confirmed price listing yet/i)).toBeInTheDocument()
  await userEvent.click(screen.getByRole('button', { name: 'Add to collection' }))
  expect(await screen.findByText('entry-detail')).toBeInTheDocument()

  const post = fetchMock.mock.calls.find((c) => (c[0] as Request).method === 'POST' && requestPath(c[0]) === '/api/entries')
  const body = await putBody<Record<string, unknown>>(post?.[0])
  expect(body.product_id).toBe(communityProduct.id)
  expect(fetchMock.mock.calls.some((c) => requestPath(c[0]) === '/api/products/resolve')).toBe(false)
})

it('groups the details region select from the picked chip and defaults platform-first', async () => {
  const groupedAnswer = {
    degraded: false,
    results: [{
      type: 'game', name: 'Secret of Mana', igdb_game_id: 1001,
      matched_region: 'ja-JP',
      localizations: [{ region: 'ja-JP', translit: 'Seiken Densetsu 2' }],
      platforms: [{ igdb_platform_id: 6, name: 'SNES', release_regions: ['north_america', 'europe'] }],
    }],
  }
  const fetchMock = vi.fn().mockImplementation((url: unknown) => {
    const u = requestPath(url)
    if (u.startsWith('/api/search')) return Promise.resolve(jsonResponse(200, groupedAnswer))
    return Promise.resolve(jsonResponse(404, {}))
  })
  vi.stubGlobal('fetch', fetchMock)
  renderWizard()

  await userEvent.type(screen.getByRole('searchbox', { name: /search/i }), 'seiken')
  await userEvent.click(screen.getByRole('button', { name: 'Search' }))
  await userEvent.click(await screen.findByRole('button', { name: 'Secret of Mana on SNES (NTSC-U/PAL)' }))

  // Platform-first: ja-JP is not in the SNES chip's set, so the
  // earliest chip region wins and the heading stays canonical.
  expect(await screen.findByRole('heading', { name: 'Your copy of Secret of Mana' })).toBeInTheDocument()
  const select = screen.getByLabelText('Region')
  expect(select).toHaveValue('ntsc_u')
  expect(select.querySelectorAll('optgroup')[0]).toHaveAttribute('label', 'Released on SNES')
})

it('restores the typed search when Back returns from the details step', async () => {
  const fetchMock = vi.fn().mockImplementation((url: unknown) => {
    const u = requestPath(url)
    if (u.startsWith('/api/search')) return Promise.resolve(jsonResponse(200, searchAnswer))
    return Promise.resolve(jsonResponse(404, {}))
  })
  vi.stubGlobal('fetch', fetchMock)
  renderWizard()

  await userEvent.type(screen.getByRole('searchbox', { name: /search/i }), 'chrono')
  await userEvent.click(screen.getByRole('button', { name: 'Search' }))
  await userEvent.click(await screen.findByRole('button', { name: 'Chrono Trigger on SNES' }))
  await userEvent.click(await screen.findByRole('button', { name: 'Back' }))

  // The query and its results are back without retyping anything.
  expect(await screen.findByRole('searchbox', { name: /search/i })).toHaveValue('chrono')
  expect(await screen.findByText('Chrono Trigger')).toBeInTheDocument()
})

it('prefers the wizard snapshot over the q deep link on Back', async () => {
  // mockImplementation, not mockResolvedValue: this test's two distinct
  // searches ('zelda' then 'mario') both hit the mock for real, and a
  // Response body can only be read once - a single shared instance
  // would fail the second read.
  const fetchMock = vi.fn().mockImplementation(() => Promise.resolve(jsonResponse(200, searchAnswer)))
  vi.stubGlobal('fetch', fetchMock)
  renderWizard('/add?q=zelda')

  // The deep link auto-ran; the user then searches something else.
  expect(await screen.findByText('Chrono Trigger')).toBeInTheDocument()
  await userEvent.clear(screen.getByRole('searchbox', { name: /search/i }))
  await userEvent.type(screen.getByRole('searchbox', { name: /search/i }), 'mario')
  await userEvent.click(screen.getByRole('button', { name: 'Search' }))
  await userEvent.click(await screen.findByRole('button', { name: 'Chrono Trigger on SNES' }))
  await userEvent.click(await screen.findByRole('button', { name: 'Back' }))

  expect(await screen.findByRole('searchbox', { name: /search/i })).toHaveValue('mario')
})

it('seeds the custom step with an accessory item type and the typed text from a hardware-tab search', async () => {
  const fetchMock = vi.fn().mockImplementation((url: unknown) => {
    const u = requestPath(url)
    if (u.startsWith('/api/search')) return Promise.resolve(jsonResponse(200, { degraded: false, results: [] }))
    return Promise.resolve(jsonResponse(404, {}))
  })
  vi.stubGlobal('fetch', fetchMock)
  renderWizard()

  await userEvent.click(screen.getByRole('radio', { name: /hardware/i }))
  await userEvent.type(screen.getByRole('searchbox', { name: /search/i }), 'link cable')
  await userEvent.click(screen.getByRole('button', { name: 'Search' }))
  expect(await screen.findByText(/no results/i)).toBeInTheDocument()

  await userEvent.click(screen.getByRole('button', { name: /add it as a custom item/i }))
  expect(screen.getByLabelText(/^name$/i)).toHaveValue('link cable')
  expect(screen.getByLabelText(/item type/i)).toHaveValue('accessory')
})

it('carries the custom step region choice into the details step defaults', async () => {
  vi.stubGlobal('fetch', vi.fn())
  renderWizard()

  await userEvent.click(screen.getByRole('button', { name: /add it as a custom item/i }))
  await userEvent.type(screen.getByLabelText(/^name$/i), 'Homebrew Cart')
  fireEvent.change(screen.getByLabelText('Region'), { target: { value: 'pal' } })
  await userEvent.click(screen.getByRole('button', { name: 'Continue' }))

  expect(await screen.findByLabelText('Region')).toHaveValue('pal')
})
