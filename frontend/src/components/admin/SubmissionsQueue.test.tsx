import { i18n } from '@lingui/core'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { act, cleanup, screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { MemoryRouter } from 'react-router'
import { messages as jaMessages } from '../../locales/ja.po'
import { jsonResponse, problemResponse, requestPath } from '../../test/fixtures'
import { renderWithI18n } from '../../test/i18n'
import SubmissionsQueue from './SubmissionsQueue'

function renderQueue() {
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false, staleTime: Infinity }, mutations: { retry: false } } })
  // ReviewPanel's PlatformPicker fires a ['platforms'] fetch on mount; seed it
  // fresh+stale-proof so it never consumes a slot from the order-sensitive
  // submissions-list/verdict route queues below.
  qc.setQueryData(['platforms'], { platforms: [] })
  // SubmitterCell's Link throws outside a Router, so every render needs
  // one, not just tests asserting on a link.
  renderWithI18n(
    <QueryClientProvider client={qc}>
      <MemoryRouter>
        <SubmissionsQueue />
      </MemoryRouter>
    </QueryClientProvider>,
  )
}

// Routed by first matching prefix; an unstubbed URL fails the test in afterEach.
// A route's value is a plain body (200), a Response (explicit status), or an
// array consumed in call order (last entry repeats once exhausted).
// '.../submissions?' and '.../submissions/' are distinct prefixes (list always
// has a query string, verdict is always a sub-path) so they never collide.
let unstubbed: string[] = []
function stubFetch(routes: Record<string, unknown>) {
  const counts: Record<string, number> = {}
  const impl = vi.fn().mockImplementation((url: unknown) => {
    const hit = Object.entries(routes).find(([prefix]) => requestPath(url).startsWith(prefix))
    if (!hit) {
      unstubbed.push(requestPath(url))
      return Promise.reject(new Error(`unstubbed fetch: ${requestPath(url)}`))
    }
    const [prefix, entry] = hit
    const sequence = Array.isArray(entry) ? entry : [entry]
    const n = counts[prefix] ?? 0
    counts[prefix] = n + 1
    const value: unknown = sequence[Math.min(n, sequence.length - 1)]
    return Promise.resolve(value instanceof Response ? value : jsonResponse(200, value))
  })
  vi.stubGlobal('fetch', impl)
  return impl
}

afterEach(() => {
  vi.unstubAllGlobals()
  // Shared singleton; leave en active for the suite. Unmount first, ahead of
  // RTL's cleanup, else I18nProvider updates outside act.
  cleanup()
  i18n.activate('en')
  const missed = unstubbed
  unstubbed = []
  expect(missed).toEqual([])
})

const row = (id: string, name: string) => ({
  id, entry_id: `e-${id}`, user_id: 'u1', status: 'pending',
  display_name: name, item_type: 'game', platform_name: 'snes',
  region: 'pal', edition: 'glow cart',
  created_at: '2026-07-17T00:00:00Z', updated_at: '2026-07-17T00:00:00Z',
})

// No cards resolved; fine since none of these tests assert on the submitter cell.
const noCards = { profiles: [] }

// raceVerdict opens the row's panel and approves it into the 409.
async function raceVerdict() {
  const user = userEvent.setup()
  await user.click(await screen.findByRole('button', { name: 'Review' }))
  await user.click(screen.getByRole('button', { name: 'Approve as new product' }))
}

it("renders the total heading and a row's proposal fields", async () => {
  stubFetch({
    '/api/admin/submissions?': { submissions: [row('s1', 'Repro Alpha')], total_count: 1 },
    '/api/shared/profiles/by-ids': noCards,
  })
  renderQueue()
  expect(await screen.findByText('1 pending submission')).toBeInTheDocument()
  expect(screen.getByText('Repro Alpha')).toBeInTheDocument()
  expect(screen.getByText('game')).toBeInTheDocument()
  expect(screen.getByText('snes')).toBeInTheDocument()
  // 'PAL', not the raw stored 'pal' - regionLabelText's display label.
  expect(screen.getByText('PAL / glow cart')).toBeInTheDocument()
  expect(screen.queryByRole('button', { name: 'Load more' })).not.toBeInTheDocument()
})

it('pluralizes the count heading (singular at one, plural above)', async () => {
  stubFetch({
    '/api/admin/submissions?': { submissions: [row('s1', 'Repro Alpha'), row('s2', 'Repro Beta')], total_count: 2 },
    '/api/shared/profiles/by-ids': noCards,
  })
  renderQueue()
  expect(await screen.findByText('2 pending submissions')).toBeInTheDocument()
})

it('shows a known region by its display label, not the raw stored code', async () => {
  stubFetch({
    '/api/admin/submissions?': { submissions: [{ ...row('s1', 'Repro Alpha'), region: 'ntsc_u' }], total_count: 1 },
    '/api/shared/profiles/by-ids': noCards,
  })
  renderQueue()
  expect(await screen.findByText('NTSC-U / glow cart')).toBeInTheDocument()
})

it("links a listed or unlisted submitter's handle to their profile and shows a private submitter's handle as plain text", async () => {
  stubFetch({
    '/api/admin/submissions?': {
      submissions: [
        { ...row('s1', 'Repro Alpha'), user_id: 'alice-id' },
        { ...row('s2', 'Repro Beta'), user_id: 'bob-id' },
        { ...row('s3', 'Repro Gamma'), user_id: 'carol-id' },
      ],
      total_count: 3,
    },
    '/api/shared/profiles/by-ids': {
      profiles: [
        { user_id: 'alice-id', handle: 'alice', profile_visibility: 'listed' },
        { user_id: 'bob-id', handle: 'bob', profile_visibility: 'private' },
        { user_id: 'carol-id', handle: 'carol', profile_visibility: 'unlisted' },
      ],
    },
  })
  renderQueue()
  const aliceLink = await screen.findByRole('link', { name: 'alice' })
  expect(aliceLink).toHaveAttribute('href', '/u/alice')
  // unlisted is not private: it still links, same as listed.
  const carolLink = await screen.findByRole('link', { name: 'carol' })
  expect(carolLink).toHaveAttribute('href', '/u/carol')
  expect(screen.getByText('bob')).toBeInTheDocument()
  expect(screen.queryByRole('link', { name: 'bob' })).not.toBeInTheDocument()
})

it('falls back to the short user id when the profile cards query errors', async () => {
  stubFetch({
    '/api/admin/submissions?': { submissions: [{ ...row('s1', 'Repro Alpha'), user_id: 'abcdefgh-1234' }], total_count: 1 },
    '/api/shared/profiles/by-ids': problemResponse(502),
  })
  renderQueue()
  await screen.findByText('Repro Alpha')
  expect(screen.getByText('abcdefgh')).toBeInTheDocument()
})

it('opens the review panel for a row', async () => {
  stubFetch({
    '/api/admin/submissions?': { submissions: [row('s1', 'Repro Alpha')], total_count: 1 },
    '/api/shared/profiles/by-ids': noCards,
    '/api/search': { degraded: false, results: [] },
  })
  renderQueue()
  const user = userEvent.setup()
  await user.click(await screen.findByRole('button', { name: 'Review' }))
  expect(screen.getByLabelText('Review Repro Alpha')).toBeInTheDocument()
})

it('resets every prefilled field and the adopt view when the reviewed row changes (missing-key regression)', async () => {
  stubFetch({
    '/api/admin/submissions?': { submissions: [row('s1', 'Alpha One'), row('s2', 'Beta Two')], total_count: 2 },
    '/api/shared/profiles/by-ids': noCards,
    '/api/search': { degraded: false, results: [] },
    // The adopt view's SearchPicker renders prices via useDisplayMoney, which
    // loads currency+rates on mount.
    '/api/me': {},
    '/api/fx': {},
  })
  renderQueue()
  const user = userEvent.setup()
  const reviewButtons = await screen.findAllByRole('button', { name: 'Review' })
  await user.click(reviewButtons[0])
  await user.click(screen.getByRole('button', { name: 'Adopt existing product' }))
  expect(screen.getByLabelText('Product id')).toBeInTheDocument()

  // Without a key the panel would carry over the first row's open adopt
  // search and stale name field.
  await user.click(screen.getAllByRole('button', { name: 'Review' })[1])
  expect(screen.queryByLabelText('Product id')).not.toBeInTheDocument()
  expect(screen.getByLabelText('Name')).toHaveValue('Beta Two')
})

it('carries the raced-verdict message to a queue notice after the panel closes', async () => {
  stubFetch({
    '/api/admin/submissions?': { submissions: [row('s1', 'Repro Alpha')], total_count: 1 },
    '/api/admin/submissions/': problemResponse(409, 'submission_resolved', 'already resolved'),
    '/api/search': { degraded: false, results: [] },
    '/api/shared/profiles/by-ids': noCards,
  })
  renderQueue()
  await raceVerdict()
  // Panel unmounts on the raced 409 before its inline message paints; the
  // notice lives at the queue.
  const notice = await screen.findByText('Another admin already resolved this submission.')
  expect(notice).toBeInTheDocument()
  expect(notice).toHaveAttribute('role', 'status')
  expect(screen.queryByLabelText('Review Repro Alpha')).not.toBeInTheDocument()
})

it('rephrases a standing notice when the locale changes', async () => {
  stubFetch({
    '/api/admin/submissions?': { submissions: [row('s1', 'Repro Alpha')], total_count: 1 },
    '/api/admin/submissions/': problemResponse(409, 'submission_resolved', 'already resolved'),
    '/api/search': { degraded: false, results: [] },
    '/api/shared/profiles/by-ids': noCards,
  })
  renderQueue()
  await raceVerdict()
  await screen.findByText('Another admin already resolved this submission.')
  // Queue holds the error, not rendered text, so switching catalogs rewrites
  // the notice instead of leaving stale language.
  act(() => {
    i18n.load('ja', jaMessages)
    i18n.activate('ja')
  })
  expect(screen.getByText('別の管理者がこのカタログ申請をすでに処理しました。')).toBeInTheDocument()
  expect(screen.queryByText('Another admin already resolved this submission.')).toBeNull()
})

it('resolves a verdict and the row leaves the list', async () => {
  // Only the submissions-list endpoint is order-sensitive (row, then empty
  // after the post-verdict refetch); other calls match by prefix so they
  // never consume a list slot.
  stubFetch({
    '/api/admin/submissions?': [
      { submissions: [row('s1', 'Repro Alpha')], total_count: 1 },
      { submissions: [], total_count: 0 },
    ],
    '/api/admin/submissions/': {
      id: 's1', entry_id: 'e-s1', status: 'approved',
      created_at: '2026-07-17T00:00:00Z', updated_at: '2026-07-17T00:00:00Z',
    },
    '/api/search': { degraded: false, results: [] },
    '/api/shared/profiles/by-ids': noCards,
  })
  renderQueue()
  const user = userEvent.setup()
  await user.click(await screen.findByRole('button', { name: 'Review' }))
  await user.click(screen.getByRole('button', { name: 'Approve as new product' }))
  expect(await screen.findByText('0 pending submissions')).toBeInTheDocument()
  expect(screen.queryByText('Repro Alpha')).not.toBeInTheDocument()
  expect(screen.queryByLabelText('Review Repro Alpha')).not.toBeInTheDocument()
})
