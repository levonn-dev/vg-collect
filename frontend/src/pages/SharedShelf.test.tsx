import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { act, screen, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { MemoryRouter, Route, Routes } from 'react-router'
import type { ProfileCard, ShelfPage } from '../api/social'
import { UNDO_WINDOW_MS } from '../components/social/useCommentDelete'
import { fxRatesFixture, jsonResponse, meFixture, sharedEntryFixture } from '../test/fixtures'
import { renderWithI18n } from '../test/i18n'
import SharedShelf from './SharedShelf'

// Same route-map idiom as Profile.test/Explore.test: fetch is
// dispatched by matching prefix, and any URL nothing stubbed fails
// the test in afterEach. A route's value may be a plain body (always
// 200), a Response (explicit status), or an array of either consumed
// in call order (the last entry repeats once exhausted) - what the
// grouped Load-more test needs for its second, different page.
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
    '/api/profiles/alice/shelves/backlog-wall': jsonResponse(404, {
      type: 'about:blank', title: 'Not Found', status: 404, code: 'shelf_not_found',
    }),
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
      entries: [sharedEntryFixture({ display_name: 'First Up' }), sharedEntryFixture({ display_name: 'Second Up' })],
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
  // The server pages the underlying sequence before grouping, so the
  // same key can recur on a later page - here SNES continues with a
  // second title instead of starting a duplicate SNES section.
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
    ([url, init]) => String(url) === '/api/shelves/shelf1/comments' && (init as RequestInit | undefined)?.method === 'POST',
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
    // Stubbed so the still-pending c1's unmount-triggered flush (at
    // the end of this test, below) has somewhere valid to resolve -
    // it must never fire WHILE the test's own assertions run, which
    // is what the whole test verifies.
    '/api/comments/c1': new Response(null, { status: 204 }),
  })
  const { unmount } = renderShelf()
  await screen.findByText('My take')

  // Fake timers engage only around the click itself: fireEvent (not
  // userEvent) avoids userEvent's own internal delay hanging under a
  // faked clock, matching Explore.test's established pattern.
  vi.useFakeTimers({ toFake: ['setTimeout', 'clearTimeout'] })
  act(() => {
    screen.getByRole('button', { name: 'Delete your comment: My take' }).click()
  })

  expect(screen.queryByText('My take')).not.toBeInTheDocument()
  expect(screen.getByRole('status')).toHaveTextContent('Comment deleted - Undo')
  expect(fetchMock.mock.calls.some(([url]) => String(url) === '/api/comments/c1')).toBe(false)

  // Advance to just under the undo window: still no DELETE.
  act(() => {
    vi.advanceTimersByTime(UNDO_WINDOW_MS - 1000)
  })
  expect(fetchMock.mock.calls.some(([url]) => String(url) === '/api/comments/c1')).toBe(false)

  // c1 is still pending (never reached expiry, never undone).
  // Unmounting here - inside the test, while fetch is still mocked -
  // lets its unmount-triggered flush (useCommentDelete's own
  // pagehide-equivalent commit) run safely; deferring to RTL's
  // implicit auto-cleanup would hit the real fetch after this file's
  // own afterEach unstubs it.
  act(() => unmount())
})
