import { i18n } from '@lingui/core'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { act, cleanup, screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { messages as jaMessages } from '../../locales/ja.po'
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

afterEach(() => {
  vi.unstubAllGlobals()
  // Tests share the module-level singleton; leave en active for the
  // rest of this file and every other one. Unmount first: this hook
  // runs ahead of RTL's auto-cleanup, and re-activating against a
  // mounted tree is an I18nProvider update outside act.
  cleanup()
  i18n.activate('en')
})

const row = (id: string, name: string) => ({
  id, entry_id: `e-${id}`, user_id: 'u1', status: 'pending',
  display_name: name, item_type: 'game', platform_name: 'snes',
  region: 'pal', edition: 'glow cart',
  created_at: '2026-07-17T00:00:00Z', updated_at: '2026-07-17T00:00:00Z',
})

// The raced-409 shape both notice tests below drive: the verdict POST
// answers 409 submission_resolved, while the list and the panel's own
// duplicates search answer normally. URL/method-aware, not an ordered
// mockResolvedValueOnce chain: opening the review panel also fires that
// duplicates search, so call order alone cannot pick out the POST.
function stubRacedVerdictFetch() {
  vi.stubGlobal('fetch', vi.fn().mockImplementation((url: string, init?: RequestInit) => {
    const u = String(url)
    if (u.startsWith('/api/search')) return Promise.resolve(jsonResponse(200, { degraded: false, results: [] }))
    if (u.startsWith('/api/admin/submissions/') && init?.method === 'POST') {
      return Promise.resolve(jsonResponse(409, {
        type: 'about:blank', title: 'Conflict', status: 409,
        code: 'submission_resolved', detail: 'already resolved',
      }))
    }
    return Promise.resolve(jsonResponse(200, { submissions: [row('s1', 'Repro Alpha')], total_count: 1 }))
  }))
}

// raceVerdict opens the row's panel and approves it into the 409.
async function raceVerdict() {
  const user = userEvent.setup()
  await user.click(await screen.findByRole('button', { name: 'Review' }))
  await user.click(screen.getByRole('button', { name: 'Approve as new product' }))
}

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
  stubRacedVerdictFetch()
  renderQueue()
  await raceVerdict()
  // The panel unmounts on the raced 409, so its own inline message never
  // paints; the notice lives at the queue and is seen after the close.
  const notice = await screen.findByText('Another admin already resolved this submission.')
  expect(notice).toBeInTheDocument()
  expect(notice).toHaveAttribute('role', 'status')
  expect(screen.queryByLabelText('Review Repro Alpha')).not.toBeInTheDocument()
})

it('rephrases a standing notice when the locale changes', async () => {
  stubRacedVerdictFetch()
  renderQueue()
  await raceVerdict()
  await screen.findByText('Another admin already resolved this submission.')
  // The notice can outlive the language it was raised in: the queue
  // holds the error, so switching catalogs has to rewrite the text
  // rather than leave the old language on screen.
  act(() => {
    i18n.load('ja', jaMessages)
    i18n.activate('ja')
  })
  expect(screen.getByText('別の管理者がこのカタログ申請をすでに処理しました。')).toBeInTheDocument()
  expect(screen.queryByText('Another admin already resolved this submission.')).toBeNull()
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
