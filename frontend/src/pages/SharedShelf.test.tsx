import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { act, screen, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { MemoryRouter, Route, Routes } from 'react-router'
import type { ProfileCard, ShelfPage } from '../api/social'
import { UNDO_WINDOW_MS } from '../components/social/useCommentDelete'
import { fxRatesFixture, jsonResponse, meFixture, problemResponse, requestPath, sharedEntryFixture } from '../test/fixtures'
import { renderWithI18n } from '../test/i18n'
import SharedShelf from './SharedShelf'

// Same route-map idiom as Profile.test/Explore.test: prefix-matched
// dispatch, unstubbed URLs fail in afterEach. Value is a body,
// Response, or array consumed in order (last repeats), for the
// Load-more test's second page.
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
  vi.useRealTimers()
  vi.unstubAllGlobals()
  const missed = unstubbed
  unstubbed = []
  expect(missed).toEqual([])
})

const owner: ProfileCard = { user_id: 'owner1', handle: 'Alice_Prime', profile_visibility: 'listed' }

function shelfPage(overrides: Partial<ShelfPage> = {}): ShelfPage {
  return {
    shelf: {
      id: 'shelf1', name: 'Backlog Wall', slug: 'backlog-wall',
      params: { mode: 'table' }, published_at: '2026-07-01T00:00:00Z',
    },
    owner,
    social_available: true,
    social: { shelf_id: 'shelf1', like_count: 2, comment_count: 0, viewer_likes: false },
    ...overrides,
  }
}

function renderShelf(handle = 'alice', slug = 'backlog-wall') {
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } })
  return renderWithI18n(
    <QueryClientProvider client={qc}>
      <MemoryRouter initialEntries={[`/u/${handle}/shelves/${slug}`]}>
        <Routes>
          <Route path="/u/:handle/shelves/:slug" element={<SharedShelf />} />
        </Routes>
      </MemoryRouter>
    </QueryClientProvider>,
  )
}

it('shows a loading state before the shelf resolves', () => {
  vi.stubGlobal('fetch', vi.fn().mockImplementation(() => new Promise(() => {})))
  renderShelf()
  expect(screen.getByText(/loading shelf/i)).toBeInTheDocument()
})

it('renders NotFoundState for a 404 (unknown or private, indistinguishably)', async () => {
  stubFetch({
    '/api/me': meFixture(),
    '/api/profiles/alice/shelves/backlog-wall': problemResponse(404, 'shelf_not_found'),
  })
  renderShelf()
  expect(await screen.findByRole('alert')).toHaveTextContent('Nothing here.')
})

it('shows a generic error state for a non-404 failure', async () => {
  stubFetch({ '/api/me': meFixture(), '/api/profiles/alice/shelves/backlog-wall': jsonResponse(502, {}) })
  renderShelf()
  expect(await screen.findByRole('alert')).toHaveTextContent(/cannot be loaded/i)
})

it('renders the header: name, owner chip, like count, entry count, and published date', async () => {
  stubFetch({
    '/api/me': meFixture({ id: 'visitor', handle: 'visitor' }),
    '/api/profiles/alice/shelves/backlog-wall': shelfPage(),
    '/api/shelves/shelf1/entries': { total_count: 2, entries: [sharedEntryFixture(), sharedEntryFixture()] },
    '/api/shelves/shelf1/comments': { comments: [] },
    '/api/fx': fxRatesFixture(),
  })
  renderShelf()
  expect(await screen.findByRole('heading', { name: 'Backlog Wall' })).toBeInTheDocument()
  expect(screen.getByRole('link', { name: '@Alice_Prime' })).toBeInTheDocument()
  expect(screen.getByText('2')).toBeInTheDocument() // LikeButton's count
  expect(await screen.findByText('2 entries')).toBeInTheDocument()
  expect(screen.getByText(new Date('2026-07-01T00:00:00Z').toLocaleDateString())).toBeInTheDocument()
})

it('hides the like button and counts quietly when the social summary is unavailable', async () => {
  stubFetch({
    '/api/me': meFixture(),
    '/api/profiles/alice/shelves/backlog-wall': shelfPage({ social_available: false, social: undefined }),
    '/api/shelves/shelf1/entries': { total_count: 0, entries: [] },
    '/api/shelves/shelf1/comments': { comments: [] },
  })
  renderShelf()
  await screen.findByRole('heading', { name: 'Backlog Wall' })
  expect(screen.queryByRole('button', { name: /Like/ })).not.toBeInTheDocument()
})

it('shows the empty-shelf message when there are no entries', async () => {
  stubFetch({
    '/api/me': meFixture(),
    '/api/profiles/alice/shelves/backlog-wall': shelfPage(),
    '/api/shelves/shelf1/entries': { total_count: 0, entries: [] },
    '/api/shelves/shelf1/comments': { comments: [] },
  })
  renderShelf()
  expect(await screen.findByText('This shelf is empty.')).toBeInTheDocument()
})

it('renders entries read-only, numbered 1-based, for a rank-sorted table stub', async () => {
  stubFetch({
    '/api/me': meFixture({ id: 'visitor', handle: 'visitor' }),
    '/api/profiles/alice/shelves/backlog-wall': shelfPage({
      shelf: { id: 'shelf1', name: 'Backlog Wall', slug: 'backlog-wall', params: { mode: 'table', sort: 'backlog_rank' } },
    }),
    '/api/shelves/shelf1/entries': {
      total_count: 2,
      entries: [
        sharedEntryFixture({ display_name: 'First Up' }),
        // JP-trio fixture: see EntryTable.test.tsx / productTitle.test.ts.
        sharedEntryFixture({
          display_name: 'Trials of Mana',
          localized_name: '聖剣伝説 3',
          localized_name_translit: 'Seiken Densetsu 3',
          localized_cover_url: 'https://x/jp.jpg',
          region: 'ntsc_j',
        }),
      ],
    },
    '/api/shelves/shelf1/comments': { comments: [] },
    '/api/fx': fxRatesFixture(),
  })
  renderShelf()
  expect(await screen.findByText('First Up')).toBeInTheDocument()

  // read-only: nothing links into /entries/:id anywhere on the page
  const links = screen.getAllByRole('link')
  expect(links.some((a) => (a.getAttribute('href') ?? '').startsWith('/entries/'))).toBe(false)

  const rows = screen.getAllByRole('row').slice(1) // drop the header row
  expect(within(rows[0]).getByRole('cell', { name: '1' })).toBeInTheDocument()
  expect(within(rows[1]).getByRole('cell', { name: '2' })).toBeInTheDocument()

  // Second row's region-localized title passes through unchanged:
  // romanized text, tagged ja-Latn.
  expect(within(rows[1]).getByText('Seiken Densetsu 3')).toHaveAttribute('lang', 'ja-Latn')
})

it('renders no selection checkboxes, even in table mode (bulk edit is owner-only)', async () => {
  stubFetch({
    '/api/me': meFixture(),
    '/api/profiles/alice/shelves/backlog-wall': shelfPage(),
    '/api/shelves/shelf1/entries': { total_count: 1, entries: [sharedEntryFixture({ display_name: 'Chrono Trigger' })] },
    '/api/shelves/shelf1/comments': { comments: [] },
    '/api/fx': fxRatesFixture(),
  })
  renderShelf()
  await screen.findByText('Chrono Trigger')
  expect(screen.getByRole('table')).toBeInTheDocument()
  expect(screen.queryByRole('checkbox')).not.toBeInTheDocument()
})

it('numbers grouped entries too when the shelf is backlog_rank-sorted', async () => {
  stubFetch({
    '/api/me': meFixture(),
    '/api/profiles/alice/shelves/backlog-wall': shelfPage({
      shelf: {
        id: 'shelf1', name: 'Backlog Wall', slug: 'backlog-wall',
        params: { mode: 'table', sort: 'backlog_rank', group_by: 'platform' },
      },
    }),
    '/api/shelves/shelf1/entries': {
      total_count: 1,
      groups: [{ key: 'snes', label: 'SNES', entries: [sharedEntryFixture({ display_name: 'Chrono Trigger' })] }],
    },
    '/api/shelves/shelf1/comments': { comments: [] },
    '/api/fx': fxRatesFixture(),
  })
  renderShelf()
  expect(await screen.findByRole('heading', { name: 'SNES' })).toBeInTheDocument()
  const rows = screen.getAllByRole('row').slice(1) // drop the header row
  expect(within(rows[0]).getByRole('cell', { name: '1' })).toBeInTheDocument()
})

it('renders grouped entries as labeled sections', async () => {
  stubFetch({
    '/api/me': meFixture(),
    '/api/profiles/alice/shelves/backlog-wall': shelfPage({
      shelf: { id: 'shelf1', name: 'Backlog Wall', slug: 'backlog-wall', params: { mode: 'compact', group_by: 'platform' } },
    }),
    '/api/shelves/shelf1/entries': {
      total_count: 1,
      groups: [{ key: 'snes', label: 'SNES', entries: [sharedEntryFixture({ display_name: 'Chrono Trigger' })] }],
    },
    '/api/shelves/shelf1/comments': { comments: [] },
    '/api/fx': fxRatesFixture(),
  })
  renderShelf()
  expect(await screen.findByRole('heading', { name: 'SNES' })).toBeInTheDocument()
  expect(screen.getByText('Chrono Trigger')).toBeInTheDocument()
})

it('merges a Load more page into an already-open group instead of duplicating or replacing its section', async () => {
  const first = {
    total_count: 2,
    groups: [{ key: 'snes', label: 'SNES', entries: [sharedEntryFixture({ display_name: 'Chrono Trigger' })] }],
  }
  // Server pages before grouping, so the same key recurs on a later
  // page; SNES continues, no duplicate section.
  const second = {
    total_count: 2,
    groups: [{ key: 'snes', label: 'SNES', entries: [sharedEntryFixture({ display_name: 'Super Mario World' })] }],
  }
  stubFetch({
    '/api/me': meFixture(),
    '/api/profiles/alice/shelves/backlog-wall': shelfPage({
      shelf: { id: 'shelf1', name: 'Backlog Wall', slug: 'backlog-wall', params: { mode: 'compact', group_by: 'platform' } },
    }),
    '/api/shelves/shelf1/entries': [first, second],
    '/api/shelves/shelf1/comments': { comments: [] },
    '/api/fx': fxRatesFixture(),
  })
  renderShelf()
  expect(await screen.findByText('Chrono Trigger')).toBeInTheDocument()
  expect(screen.queryByText('Super Mario World')).not.toBeInTheDocument()

  await userEvent.click(screen.getByRole('button', { name: 'Load more' }))
  expect(await screen.findByText('Super Mario World')).toBeInTheDocument()
  // Still exactly one SNES section, not two.
  expect(screen.getAllByRole('heading', { name: 'SNES' })).toHaveLength(1)
})

it('renders a flat, ungrouped list in a non-table mode through the plain View branch', async () => {
  stubFetch({
    '/api/me': meFixture(),
    '/api/profiles/alice/shelves/backlog-wall': shelfPage({
      shelf: { id: 'shelf1', name: 'Backlog Wall', slug: 'backlog-wall', params: { mode: 'grid' } },
    }),
    '/api/shelves/shelf1/entries': {
      total_count: 1,
      entries: [sharedEntryFixture({ display_name: 'Chrono Trigger' })],
    },
    '/api/shelves/shelf1/comments': { comments: [] },
    '/api/fx': fxRatesFixture(),
  })
  renderShelf()
  expect(await screen.findByText('Chrono Trigger')).toBeInTheDocument()
  expect(screen.queryByRole('link', { name: /Chrono Trigger/ })).not.toBeInTheDocument()
})

it('posts a new comment through the composer', async () => {
  const fetchMock = stubFetch({
    '/api/me': meFixture({ id: 'visitor', handle: 'visitor' }),
    '/api/profiles/alice/shelves/backlog-wall': shelfPage(),
    '/api/shelves/shelf1/entries': { total_count: 0, entries: [] },
    '/api/shelves/shelf1/comments': { comments: [] },
  })
  renderShelf()
  const box = await screen.findByRole('textbox', { name: 'Add a comment' })
  await userEvent.type(box, 'Nice shelf!')
  await userEvent.click(screen.getByRole('button', { name: 'Post' }))
  const posted = fetchMock.mock.calls.find(
    ([url]) => requestPath(url) === '/api/shelves/shelf1/comments' && (url as Request).method === 'POST',
  )
  expect(posted).toBeDefined()
})

it('shows the undo toast and fires no immediate DELETE when the viewer deletes their own comment', async () => {
  const fetchMock = stubFetch({
    '/api/me': meFixture({ id: 'visitor', handle: 'visitor' }),
    '/api/profiles/alice/shelves/backlog-wall': shelfPage(),
    '/api/shelves/shelf1/entries': { total_count: 0, entries: [] },
    '/api/shelves/shelf1/comments': {
      comments: [{ id: 'c1', shelf_id: 'shelf1', author_id: 'visitor', body: 'My take', created_at: new Date().toISOString() }],
    },
    // Stubbed so c1's unmount-triggered flush (below) has somewhere to
    // resolve; must never fire during the test's own assertions.
    '/api/comments/c1': new Response(null, { status: 204 }),
  })
  const { unmount } = renderShelf()
  await screen.findByText('My take')

  // Fake timers only around the click; fireEvent avoids userEvent's
  // delay hanging under a faked clock (Explore.test pattern).
  vi.useFakeTimers({ toFake: ['setTimeout', 'clearTimeout'] })
  act(() => {
    screen.getByRole('button', { name: 'Delete your comment: My take' }).click()
  })

  expect(screen.queryByText('My take')).not.toBeInTheDocument()
  expect(screen.getByRole('status')).toHaveTextContent('Comment deleted - Undo')
  expect(fetchMock.mock.calls.some(([url]) => requestPath(url) === '/api/comments/c1')).toBe(false)

  // Advance to just under the undo window: still no DELETE.
  act(() => {
    vi.advanceTimersByTime(UNDO_WINDOW_MS - 1000)
  })
  expect(fetchMock.mock.calls.some(([url]) => requestPath(url) === '/api/comments/c1')).toBe(false)

  // c1 still pending; unmounting here (fetch still mocked) lets
  // useCommentDelete's flush run safely, before afterEach unstubs fetch.
  act(() => unmount())
})
