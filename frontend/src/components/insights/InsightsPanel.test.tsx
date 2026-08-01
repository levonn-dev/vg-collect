import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { MemoryRouter } from 'react-router'
import type { ListState } from '../../lib/listParams'
import { defaultListState } from '../../lib/listParams'
import { dashboardFixture, jsonResponse } from '../../test/fixtures'
import { renderWithI18n } from '../../test/i18n'
import InsightsPanel from './InsightsPanel'

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
    if (u.startsWith('/api/dashboard/value-history')) {
      return Promise.resolve(jsonResponse(200, overrides.history ?? history))
    }
    if (u.startsWith('/api/dashboard')) {
      return Promise.resolve(jsonResponse(200, overrides.dashboard ?? dashboardFixture()))
    }
    if (u.startsWith('/api/recommendations')) {
      return Promise.resolve(jsonResponse(200, overrides.recs ?? recs))
    }
    return Promise.resolve(jsonResponse(404, {}))
  }))
}

function renderPanel(state: ListState = defaultListState()) {
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } })
  return renderWithI18n(
    <QueryClientProvider client={qc}>
      <MemoryRouter>
        <InsightsPanel state={state} />
      </MemoryRouter>
    </QueryClientProvider>,
  )
}

afterEach(() => vi.unstubAllGlobals())

it('renders the stat cards', async () => {
  stubApi()
  renderPanel()
  expect(await screen.findByText('42')).toBeInTheDocument()
  expect(screen.getByText('$3,842.00')).toBeInTheDocument()
  expect(screen.getByText(/35 priced/)).toBeInTheDocument()
  expect(screen.getByText('$2,100.00')).toBeInTheDocument()
})

it('requests the dashboard for the active filters only', async () => {
  stubApi()
  renderPanel({ ...defaultListState(), status: ['backlog'], platformId: [6], sort: 'value', page: 3 })
  await screen.findByText('42')
  const urls = vi.mocked(fetch).mock.calls.map((c) => String(c[0] as string))
  const dash = urls.find((u) => u.startsWith('/api/dashboard') && !u.includes('value-history'))
  // Filter dimensions ride along; sort and paging must not.
  expect(dash).toContain('status=backlog')
  expect(dash).toContain('platform_id=6')
  expect(dash).not.toContain('sort=')
  expect(dash).not.toContain('offset=')
})

it('expands to the breakdowns, value history, and recommendations on demand', async () => {
  stubApi()
  renderPanel()
  await screen.findByText('42')
  // Collapsed: the heavier reads have not fired.
  let urls = vi.mocked(fetch).mock.calls.map((c) => String(c[0] as string))
  expect(urls.some((u) => u.includes('value-history'))).toBe(false)
  expect(urls.some((u) => u.includes('/api/recommendations'))).toBe(false)

  await userEvent.click(screen.getByRole('button', { name: 'Show insights' }))
  expect(await screen.findByRole('region', { name: 'By platform' })).toBeInTheDocument()
  expect(await screen.findByText('Secret of Mana')).toBeInTheDocument()
  expect(screen.getByRole('link', { name: /see all recommendations/i })).toHaveAttribute('href', '/recommendations')
  expect(await screen.findByRole('region', { name: 'Collection value over time' })).toBeInTheDocument()
  urls = vi.mocked(fetch).mock.calls.map((c) => String(c[0] as string))
  expect(urls.some((u) => u.includes('value-history'))).toBe(true)
})

it('degrades the value card when pricing is unavailable', async () => {
  stubApi({
    dashboard: dashboardFixture({
      pricing: { available: false, priced_entries: 0, unpriced_entries: 0, excluded_entries: 0 },
    }),
  })
  renderPanel()
  expect(await screen.findByRole('alert')).toHaveTextContent(/value unavailable right now/i)
})

it('reports when stats cannot be loaded without blocking the page', async () => {
  vi.stubGlobal('fetch', vi.fn().mockResolvedValue(jsonResponse(500, {})))
  renderPanel()
  expect(await screen.findByRole('alert')).toHaveTextContent(/stats cannot be loaded/i)
})

it('reports when the value history fails to load without hiding the rest', async () => {
  vi.stubGlobal('fetch', vi.fn().mockImplementation((url: string) => {
    const u = String(url)
    if (u.startsWith('/api/dashboard/value-history')) return Promise.resolve(jsonResponse(500, {}))
    if (u.startsWith('/api/dashboard')) return Promise.resolve(jsonResponse(200, dashboardFixture()))
    if (u.startsWith('/api/recommendations')) return Promise.resolve(jsonResponse(200, recs))
    return Promise.resolve(jsonResponse(404, {}))
  }))
  renderPanel()
  await screen.findByText('42')
  await userEvent.click(screen.getByRole('button', { name: 'Show insights' }))
  expect(await screen.findByRole('alert')).toHaveTextContent(/value history cannot be loaded/i)
  // The rest of the expanded section still renders.
  expect(await screen.findByRole('region', { name: 'By platform' })).toBeInTheDocument()
})
