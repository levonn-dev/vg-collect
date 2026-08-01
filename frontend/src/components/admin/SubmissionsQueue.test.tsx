import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { jsonResponse } from '../../test/fixtures'
import { renderWithI18n } from '../../test/i18n'
import SubmissionsQueue from './SubmissionsQueue'

function renderQueue() {
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false, staleTime: Infinity }, mutations: { retry: false } } })
  // Reviewing a row mounts ReviewPanel, whose PlatformPicker fires a
  // ['platforms'] fetch on mount. Several tests below queue exact,
  // order-sensitive fetch responses (mockResolvedValueOnce chains) for
  // the submissions-list and verdict calls; seed platforms fresh+stale-
  // proof so that extra fetch never consumes one of those queue slots.
  qc.setQueryData(['platforms'], { platforms: [] })
  renderWithI18n(
    <QueryClientProvider client={qc}>
      <SubmissionsQueue />
    </QueryClientProvider>,
  )
}

afterEach(() => vi.unstubAllGlobals())

const row = (id: string, name: string) => ({
  id, entry_id: `e-${id}`, user_id: 'u1', status: 'pending',
  display_name: name, item_type: 'game', platform_name: 'snes',
  region: 'pal', edition: 'glow cart',
  created_at: '2026-07-17T00:00:00Z', updated_at: '2026-07-17T00:00:00Z',
})

it("renders the total heading and a row's proposal fields", async () => {
  vi.stubGlobal('fetch', vi.fn().mockResolvedValue(jsonResponse(200, {
    submissions: [row('s1', 'Repro Alpha')],
    total_count: 1,
  })))
  renderQueue()
  expect(await screen.findByText('1 pending submission')).toBeInTheDocument()
  expect(screen.getByText('Repro Alpha')).toBeInTheDocument()
  expect(screen.getByText('game')).toBeInTheDocument()
  expect(screen.getByText('snes')).toBeInTheDocument()
  expect(screen.getByText('pal / glow cart')).toBeInTheDocument()
  expect(screen.queryByRole('button', { name: 'Load more' })).not.toBeInTheDocument()
})

it('pluralizes the count heading (singular at one, plural above)', async () => {
  vi.stubGlobal('fetch', vi.fn().mockResolvedValue(jsonResponse(200, {
    submissions: [row('s1', 'Repro Alpha'), row('s2', 'Repro Beta')],
    total_count: 2,
  })))
  renderQueue()
  expect(await screen.findByText('2 pending submissions')).toBeInTheDocument()
})

it('opens the review panel for a row', async () => {
  vi.stubGlobal('fetch', vi.fn().mockResolvedValue(jsonResponse(200, {
    submissions: [row('s1', 'Repro Alpha')], total_count: 1,
  })))
  renderQueue()
  const user = userEvent.setup()
  await user.click(await screen.findByRole('button', { name: 'Review' }))
  expect(screen.getByLabelText('Review Repro Alpha')).toBeInTheDocument()
})

it('carries the raced-verdict message to a queue notice after the panel closes', async () => {
  // URL/method-aware, not an ordered mockResolvedValueOnce chain: opening
  // the review panel now also fires its own potential-duplicates search, so
  // call order alone can no longer pick out the verdict POST.
  const fetchMock = vi.fn().mockImplementation((url: string, init?: RequestInit) => {
    const u = String(url)
    if (u.startsWith('/api/search')) return Promise.resolve(jsonResponse(200, { degraded: false, results: [] }))
    if (u.startsWith('/api/admin/submissions/') && init?.method === 'POST') {
      return Promise.resolve(jsonResponse(409, {
        type: 'about:blank', title: 'Conflict', status: 409,
        code: 'submission_resolved', detail: 'already resolved',
      }))
    }
    return Promise.resolve(jsonResponse(200, { submissions: [row('s1', 'Repro Alpha')], total_count: 1 }))
  })
  vi.stubGlobal('fetch', fetchMock)
  renderQueue()
  const user = userEvent.setup()
  await user.click(await screen.findByRole('button', { name: 'Review' }))
  await user.click(screen.getByRole('button', { name: 'Approve as new product' }))
  // The panel unmounts on the raced 409, so its own inline message never
  // paints; the notice lives at the queue and is seen after the close.
  const notice = await screen.findByText('Another admin already resolved this submission.')
  expect(notice).toBeInTheDocument()
  expect(notice).toHaveAttribute('role', 'status')
  expect(screen.queryByLabelText('Review Repro Alpha')).not.toBeInTheDocument()
})

it('resolves a verdict and the row leaves the list', async () => {
  // The submissions-list endpoint alone stays order-sensitive (row present,
  // then empty after the post-verdict invalidation refetch); the panel's own
  // duplicates search and the verdict POST are matched by URL/method so
  // they never consume one of the list's ordered slots.
  let listCalls = 0
  const fetchMock = vi.fn().mockImplementation((url: string, init?: RequestInit) => {
    const u = String(url)
    if (u.startsWith('/api/search')) return Promise.resolve(jsonResponse(200, { degraded: false, results: [] }))
    if (u.startsWith('/api/admin/submissions/') && init?.method === 'POST') {
      return Promise.resolve(jsonResponse(200, {
        id: 's1', entry_id: 'e-s1', status: 'approved',
        created_at: '2026-07-17T00:00:00Z', updated_at: '2026-07-17T00:00:00Z',
      }))
    }
    listCalls++
    return Promise.resolve(jsonResponse(200,
      listCalls === 1
        ? { submissions: [row('s1', 'Repro Alpha')], total_count: 1 }
        : { submissions: [], total_count: 0 },
    ))
  })
  vi.stubGlobal('fetch', fetchMock)
  renderQueue()
  const user = userEvent.setup()
  await user.click(await screen.findByRole('button', { name: 'Review' }))
  await user.click(screen.getByRole('button', { name: 'Approve as new product' }))
  expect(await screen.findByText('0 pending submissions')).toBeInTheDocument()
  expect(screen.queryByText('Repro Alpha')).not.toBeInTheDocument()
  expect(screen.queryByLabelText('Review Repro Alpha')).not.toBeInTheDocument()
})
