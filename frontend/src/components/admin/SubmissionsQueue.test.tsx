import { i18n } from '@lingui/core'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { act, cleanup, screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { MemoryRouter } from 'react-router'
import { messages as jaMessages } from '../../locales/ja.po'
import { jsonResponse, problemResponse } from '../../test/fixtures'
import { renderWithI18n } from '../../test/i18n'
import SubmissionsQueue from './SubmissionsQueue'

function renderQueue() {
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false, staleTime: Infinity }, mutations: { retry: false } } })
  // Reviewing a row mounts ReviewPanel, whose PlatformPicker fires a
  // ['platforms'] fetch on mount. Several tests below queue exact,
  // order-sensitive fetch responses (the submissions-list route below)
  // for the submissions-list and verdict calls; seed platforms
  // fresh+stale-proof so that extra fetch never consumes one of those
  // routes' slots.
  qc.setQueryData(['platforms'], { platforms: [] })
  // SubmitterCell links a listed card to /u/:handle via react-router's
  // Link, which throws outside a Router - every render needs one, not
  // just the tests that assert on a rendered link.
  renderWithI18n(
    <QueryClientProvider client={qc}>
      <MemoryRouter>
        <SubmissionsQueue />
      </MemoryRouter>
    </QueryClientProvider>,
  )
}

// Fetch is routed per endpoint (first matching prefix wins) so each test
// declares exactly the calls it expects; a URL nothing stubbed is
// recorded and fails the test in afterEach (Admin.test's idiom). A
// route's value may be a plain body (always 200), a Response (explicit
// status), or an array of either consumed in call order - the last
// entry repeats once exhausted, which the multi-page verdict test below
// needs. '/api/admin/submissions?' and '/api/admin/submissions/' are
// deliberately distinct prefixes: the list call always carries a query
// string (offset=...) and the verdict call is always a sub-path
// (/{id}/verdict), so the two never collide regardless of key order.
let unstubbed: string[] = []
function stubFetch(routes: Record<string, unknown>) {
  const counts: Record<string, number> = {}
  const impl = vi.fn().mockImplementation((url: string) => {
    const hit = Object.entries(routes).find(([prefix]) => String(url).startsWith(prefix))
    if (!hit) {
      unstubbed.push(String(url))
      return Promise.reject(new Error(`unstubbed fetch: ${String(url)}`))
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
  // Tests share the module-level singleton; leave en active for the
  // rest of this file and every other one. Unmount first: this hook
  // runs ahead of RTL's auto-cleanup, and re-activating against a
  // mounted tree is an I18nProvider update outside act.
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

// No cards resolved: every row in these tests falls back to its short
// id, which is fine - none of them assert on the submitter cell.
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
    // The adopt view's SearchPicker renders prices via useDisplayMoney,
    // which unconditionally loads the viewer's currency + rates on mount.
    '/api/me': {},
    '/api/fx': {},
  })
  renderQueue()
  const user = userEvent.setup()
  const reviewButtons = await screen.findAllByRole('button', { name: 'Review' })
  await user.click(reviewButtons[0])
  await user.click(screen.getByRole('button', { name: 'Adopt existing product' }))
  expect(screen.getByLabelText('Product id')).toBeInTheDocument()

  // Reviewing a different row without closing the panel first used to
  // carry over the first row's open adopt search and stale name field,
  // since the panel mounted without a key.
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
  // The panel unmounts on the raced 409, so its own inline message never
  // paints; the notice lives at the queue and is seen after the close.
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
  // duplicates search, profile cards, and the verdict POST are matched by
  // URL prefix so they never consume one of the list's ordered slots.
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
