import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { calledPath, jsonResponse } from '../../test/fixtures'
import { renderWithI18n } from '../../test/i18n'
import UnmatchedWorklist from './UnmatchedWorklist'

function renderWorklist() {
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } })
  renderWithI18n(
    <QueryClientProvider client={qc}>
      <UnmatchedWorklist />
    </QueryClientProvider>,
  )
}

afterEach(() => vi.unstubAllGlobals())

const row = (id: string, name: string, held = false) => ({
  id, type: 'game', name,
  platform: { igdb_platform_id: 4, name: 'Nintendo 64' },
  ...(held ? { match_hold: true } : {}),
  created_at: '2026-07-01T00:00:00Z', updated_at: '2026-07-02T00:00:00Z',
})

it('renders the backlog count, rows, and the held badge', async () => {
  vi.stubGlobal('fetch', vi.fn().mockResolvedValue(jsonResponse(200, {
    products: [row('p1', 'Worklist Alpha'), row('p2', 'Worklist Held', true)],
    total_count: 2,
  })))
  renderWorklist()
  expect(await screen.findByText('2 unmatched products')).toBeInTheDocument()
  expect(screen.getByText('Worklist Alpha')).toBeInTheDocument()
  expect(screen.getByText('held')).toBeInTheDocument()
  expect(screen.queryByRole('button', { name: 'Load more' })).not.toBeInTheDocument()
})

it('loads the next page and appends', async () => {
  const first = { products: Array.from({ length: 200 }, (_, i) => row(`p${i}`, `Game ${i}`)), total_count: 201 }
  const second = { products: [row('p200', 'Game 200')], total_count: 201 }
  const fetchMock = vi
    .fn()
    .mockResolvedValueOnce(jsonResponse(200, first))
    .mockResolvedValueOnce(jsonResponse(200, second))
  vi.stubGlobal('fetch', fetchMock)
  renderWorklist()
  const user = userEvent.setup()
  await user.click(await screen.findByRole('button', { name: 'Load more' }))
  expect(await screen.findByText('Game 200')).toBeInTheDocument()
  expect(calledPath(fetchMock, 1)).toBe('/api/admin/products/unmatched?offset=200')
})

it('opens the fix panel for a row', async () => {
  vi.stubGlobal('fetch', vi.fn().mockResolvedValue(jsonResponse(200, {
    products: [row('p1', 'Worklist Alpha')], total_count: 1,
  })))
  renderWorklist()
  const user = userEvent.setup()
  await user.click(await screen.findByRole('button', { name: 'Fix mapping' }))
  expect(screen.getByLabelText('Fix mapping for Worklist Alpha')).toBeInTheDocument()
})
