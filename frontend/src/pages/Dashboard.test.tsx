import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { render, screen } from '@testing-library/react'
import { MemoryRouter } from 'react-router'
import { jsonResponse } from '../test/fixtures'
import Dashboard from './Dashboard'

const dashboard = {
  total_entries: 42,
  by_status: { backlog: 12, beaten: 20, playing: 3 },
  by_item_type: { game: 38, console: 3, accessory: 1 },
  by_platform: [
    { name: 'SNES', count: 21 },
    { name: 'PlayStation', count: 14 },
  ],
  spend: [{ currency: 'USD', total_cents: 210000 }],
  pricing: {
    available: true, total_value_cents: 384200,
    priced_entries: 35, unpriced_entries: 4, excluded_entries: 3,
  },
}
const history = {
  available: true,
  points: [{ date: '2026-07-01', value_cents: 384200 }],
}
const recs = {
  degraded: false,
  recommendations: [
    { igdb_game_id: 1011, name: 'Secret of Mana', genres: ['RPG'], score: 4.5 },
  ],
}

function stubApi(overrides: Partial<Record<'dashboard' | 'history' | 'recs', unknown>> = {}) {
  vi.stubGlobal('fetch', vi.fn().mockImplementation((url: string) => {
    const u = String(url)
    if (u === '/api/dashboard') return Promise.resolve(jsonResponse(200, overrides.dashboard ?? dashboard))
    if (u === '/api/dashboard/value-history') return Promise.resolve(jsonResponse(200, overrides.history ?? history))
    if (u === '/api/recommendations') return Promise.resolve(jsonResponse(200, overrides.recs ?? recs))
    return Promise.resolve(jsonResponse(404, {}))
  }))
}

function renderDashboard() {
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } })
  return render(
    <QueryClientProvider client={qc}>
      <MemoryRouter>
        <Dashboard />
      </MemoryRouter>
    </QueryClientProvider>,
  )
}

afterEach(() => vi.unstubAllGlobals())

it('renders the stat cards', async () => {
  stubApi()
  renderDashboard()
  expect(await screen.findByText('42')).toBeInTheDocument()
  expect(screen.getByText('$3,842.00')).toBeInTheDocument()
  expect(screen.getByText(/35 priced/)).toBeInTheDocument()
  expect(screen.getByText(/4 unpriced/)).toBeInTheDocument()
  expect(screen.getByText(/3 excluded/)).toBeInTheDocument()
  expect(screen.getByText('$2,100.00')).toBeInTheDocument()
})

it('renders breakdowns and the recommendations panel with its link', async () => {
  stubApi()
  renderDashboard()
  expect(await screen.findByRole('region', { name: 'By platform' })).toBeInTheDocument()
  expect(screen.getByText(/backlog/i)).toBeInTheDocument()
  expect(await screen.findByText('Secret of Mana')).toBeInTheDocument()
  expect(screen.getByRole('link', { name: /see all recommendations/i })).toHaveAttribute('href', '/recommendations')
})

it('degrades the value card when pricing is unavailable', async () => {
  stubApi({
    dashboard: {
      ...dashboard,
      pricing: { available: false, priced_entries: 0, unpriced_entries: 0, excluded_entries: 0 },
    },
    history: { available: false, points: [] },
  })
  renderDashboard()
  expect(await screen.findAllByRole('alert')).not.toHaveLength(0)
  expect(screen.getByText(/value unavailable right now/i)).toBeInTheDocument()
})
