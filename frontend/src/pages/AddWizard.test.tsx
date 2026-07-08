import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { render, screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { MemoryRouter, Route, Routes } from 'react-router'
import { entryFixture, jsonResponse } from '../test/fixtures'
import AddWizard from './AddWizard'

const searchAnswer = {
  degraded: false,
  results: [{
    type: 'game', name: 'Chrono Trigger', igdb_game_id: 1000,
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

function renderWizard(path = '/add', qc = new QueryClient({ defaultOptions: { queries: { retry: false } } })) {
  return {
    qc,
    ...render(
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
  const fetchMock = vi.fn().mockImplementation((url: string, init?: RequestInit) => {
    const u = String(url)
    if (u.startsWith('/api/search')) return Promise.resolve(jsonResponse(200, searchAnswer))
    if (u === '/api/products/resolve') return Promise.resolve(jsonResponse(200, product))
    if (u === '/api/entries' && init?.method === 'POST') return Promise.resolve(jsonResponse(201, created))
    return Promise.resolve(jsonResponse(404, {}))
  })
  vi.stubGlobal('fetch', fetchMock)
  renderWizard()

  await userEvent.type(screen.getByRole('searchbox', { name: /search/i }), 'chrono')
  await userEvent.click(screen.getByRole('button', { name: 'Search' }))
  await userEvent.click(await screen.findByRole('button', { name: 'Chrono Trigger on SNES' }))

  expect(await screen.findByText(/your copy of chrono trigger/i)).toBeInTheDocument()
  await userEvent.selectOptions(screen.getByLabelText('Status'), 'beaten')
  await userEvent.selectOptions(screen.getByLabelText('Rating'), '9')
  await userEvent.click(screen.getByRole('button', { name: 'Continue' }))

  // Match confidence surfaces before commitment.
  expect(await screen.findByText(/match 93%/i)).toBeInTheDocument()
  await userEvent.click(screen.getByRole('button', { name: 'Add to collection' }))
  expect(await screen.findByText('entry-detail')).toBeInTheDocument()

  const post = fetchMock.mock.calls.find((c) => (c[1] as RequestInit | undefined)?.method === 'POST' && c[0] === '/api/entries')
  const body = JSON.parse((post?.[1] as RequestInit).body as string) as Record<string, unknown>
  expect(body.product_id).toBe('p1')
  expect(body.status).toBe('beaten')
  expect(body.rating).toBe(9)
  expect(body.display_name).toBeUndefined() // catalog facts come from the product
})

it('invalidates the dashboard and recommendations caches on create', async () => {
  const created = entryFixture({ display_name: 'Chrono Trigger' })
  const fetchMock = vi.fn().mockImplementation((url: string, init?: RequestInit) => {
    const u = String(url)
    if (u.startsWith('/api/search')) return Promise.resolve(jsonResponse(200, searchAnswer))
    if (u === '/api/products/resolve') return Promise.resolve(jsonResponse(200, product))
    if (u === '/api/entries' && init?.method === 'POST') return Promise.resolve(jsonResponse(201, created))
    return Promise.resolve(jsonResponse(404, {}))
  })
  vi.stubGlobal('fetch', fetchMock)
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } })
  qc.setQueryData(['dashboard'], { stale: true })
  qc.setQueryData(['recommendations'], { stale: true })
  renderWizard('/add', qc)

  await userEvent.type(screen.getByRole('searchbox', { name: /search/i }), 'chrono')
  await userEvent.click(screen.getByRole('button', { name: 'Search' }))
  await userEvent.click(await screen.findByRole('button', { name: 'Chrono Trigger on SNES' }))
  expect(await screen.findByText(/your copy of chrono trigger/i)).toBeInTheDocument()
  await userEvent.click(screen.getByRole('button', { name: 'Continue' }))
  expect(await screen.findByText(/match 93%/i)).toBeInTheDocument()
  await userEvent.click(screen.getByRole('button', { name: 'Add to collection' }))
  expect(await screen.findByText('entry-detail')).toBeInTheDocument()

  expect(qc.getQueryState(['dashboard'])?.isInvalidated).toBe(true)
  expect(qc.getQueryState(['recommendations'])?.isInvalidated).toBe(true)
})

it('shows the match-pending state for an unmatched product', async () => {
  vi.stubGlobal('fetch', vi.fn().mockImplementation((url: string) => {
    const u = String(url)
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

it('pre-runs the q parameter (the recommendations add path)', async () => {
  const fetchMock = vi.fn().mockResolvedValue(jsonResponse(200, searchAnswer))
  vi.stubGlobal('fetch', fetchMock)
  renderWizard('/add?q=chrono')
  expect(await screen.findByText('Chrono Trigger')).toBeInTheDocument()
  expect(String(fetchMock.mock.calls[0][0])).toContain('q=chrono')
})

it('creates a custom entry with pricing disabled', async () => {
  const created = entryFixture({ display_name: 'Chrono Trigger Repro', product_id: undefined, pricing_mode: 'disabled' })
  const fetchMock = vi.fn().mockImplementation((url: string, init?: RequestInit) => {
    if (String(url) === '/api/entries' && init?.method === 'POST') {
      return Promise.resolve(jsonResponse(201, created))
    }
    return Promise.resolve(jsonResponse(404, {}))
  })
  vi.stubGlobal('fetch', fetchMock)
  renderWizard()

  await userEvent.click(screen.getByRole('button', { name: /add it as a custom item/i }))
  await userEvent.type(screen.getByLabelText(/^name$/i), 'Chrono Trigger Repro')
  await userEvent.selectOptions(screen.getByLabelText(/item type/i), 'game')
  await userEvent.type(screen.getByLabelText(/platform/i), 'SNES')
  await userEvent.click(screen.getByRole('button', { name: 'Continue' }))

  await userEvent.click(await screen.findByRole('button', { name: 'Continue' })) // details step, defaults

  expect(await screen.findByText(/start without market pricing/i)).toBeInTheDocument()
  await userEvent.click(screen.getByRole('button', { name: 'Add to collection' }))
  expect(await screen.findByText('entry-detail')).toBeInTheDocument()

  const post = fetchMock.mock.calls.find((c) => (c[1] as RequestInit | undefined)?.method === 'POST')
  const body = JSON.parse((post?.[1] as RequestInit).body as string) as Record<string, unknown>
  expect(body.product_id).toBeUndefined()
  expect(body.display_name).toBe('Chrono Trigger Repro')
  expect(body.item_type).toBe('game')
  expect(body.platform_name).toBe('SNES')
  expect(body.pricing_mode).toBe('disabled')
})

it('invalidates the dashboard and recommendations caches on a custom create', async () => {
  const created = entryFixture({ display_name: 'Chrono Trigger Repro', product_id: undefined, pricing_mode: 'disabled' })
  const fetchMock = vi.fn().mockImplementation((url: string, init?: RequestInit) => {
    if (String(url) === '/api/entries' && init?.method === 'POST') {
      return Promise.resolve(jsonResponse(201, created))
    }
    return Promise.resolve(jsonResponse(404, {}))
  })
  vi.stubGlobal('fetch', fetchMock)
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } })
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
