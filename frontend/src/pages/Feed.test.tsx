import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { screen, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { MemoryRouter } from 'react-router'
import type { FeedItem, ProfileCard, ShelfCard as ShelfCardData } from '../api/social'
import { jsonResponse } from '../test/fixtures'
import { renderWithI18n } from '../test/i18n'
import Feed from './Feed'

// Same route-map idiom as Explore.test/SharedShelf.test: fetch is
// dispatched by matching prefix, and any URL nothing stubbed fails the
// test in afterEach. A route's value may be a plain body (always
// 200), a Response (explicit status), or an array of either consumed
// in call order (the last entry repeats once exhausted) - what the
// load-more test needs for its second, different page.
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
  const missed = unstubbed
  unstubbed = []
  expect(missed).toEqual([])
})

function renderFeed() {
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } })
  return renderWithI18n(
    <QueryClientProvider client={qc}>
      <MemoryRouter>
        <Feed />
      </MemoryRouter>
    </QueryClientProvider>,
  )
}

const alice: ProfileCard = { user_id: 'u1', handle: 'Alice_Prime', profile_visibility: 'listed' }
const bob: ProfileCard = { user_id: 'u2', handle: 'Bob_Prime', profile_visibility: 'listed' }

function shelf(overrides: Partial<ShelfCardData> = {}): ShelfCardData {
  return {
    id: 's1', name: 'Backlog Wall', slug: 'backlog-wall',
    owner: alice, entry_count: 3, cover_urls: [],
    ...overrides,
  }
}

function feedItem(overrides: Partial<FeedItem> = {}): FeedItem {
  return {
    id: 'f1', verb: 'liked_shelf', created_at: '2026-07-20T00:00:00Z', actor: alice,
    ...overrides,
  }
}

it('shows a loading state before the feed resolves', () => {
  vi.stubGlobal('fetch', vi.fn().mockImplementation(() => new Promise(() => {})))
  renderFeed()
  expect(screen.getByText(/loading feed/i)).toBeInTheDocument()
})

it('shows an error state when the feed fails to load', async () => {
  stubFetch({ '/api/feed?tab=following': jsonResponse(500, {}) })
  renderFeed()
  expect(await screen.findByRole('alert')).toHaveTextContent(/cannot be loaded/i)
})

it('renders verb-shaped rows on the Following tab: two profile links for a follow, a shelf link for a like, and the excerpt for a comment', async () => {
  stubFetch({
    '/api/feed?tab=following': {
      items: [
        feedItem({ id: 'f1', verb: 'followed_user', actor: alice, followed_user: bob }),
        feedItem({ id: 'f2', verb: 'liked_shelf', actor: bob, shelf: shelf() }),
        feedItem({
          id: 'f3', verb: 'commented_shelf', actor: alice,
          shelf: shelf({ id: 's2', name: 'Hall of Fame', slug: 'hall-of-fame' }),
          comment_excerpt: 'Nice picks, love the SNES lineup!',
        }),
      ],
    },
  })
  renderFeed()

  const rows = await screen.findAllByRole('listitem')
  expect(rows).toHaveLength(3)

  // Follow row: "@Alice_Prime followed @Bob_Prime" - two distinct profile links.
  expect(within(rows[0]).getByRole('link', { name: '@Alice_Prime' })).toHaveAttribute('href', '/u/Alice_Prime')
  expect(within(rows[0]).getByRole('link', { name: '@Bob_Prime' })).toHaveAttribute('href', '/u/Bob_Prime')
  expect(within(rows[0]).getByText('followed')).toBeInTheDocument()

  // Like row: the shelf itself is the link target.
  expect(within(rows[1]).getByRole('link', { name: 'Backlog Wall' }))
    .toHaveAttribute('href', '/u/Alice_Prime/shelves/backlog-wall')
  expect(within(rows[1]).getByText('liked')).toBeInTheDocument()

  // Comment row: shelf link plus the truncated excerpt text.
  expect(within(rows[2]).getByRole('link', { name: 'Hall of Fame' }))
    .toHaveAttribute('href', '/u/Alice_Prime/shelves/hall-of-fame')
  expect(within(rows[2]).getByText('commented on')).toBeInTheDocument()
  expect(within(rows[2]).getByText('Nice picks, love the SNES lineup!')).toBeInTheDocument()
})

it('renders a published_shelf row linking the shelf', async () => {
  stubFetch({
    '/api/feed?tab=following': {
      items: [feedItem({ id: 'f4', verb: 'published_shelf', actor: bob, shelf: shelf() })],
    },
  })
  renderFeed()
  expect(await screen.findByText('published')).toBeInTheDocument()
  expect(screen.getByRole('link', { name: 'Backlog Wall' })).toHaveAttribute('href', '/u/Alice_Prime/shelves/backlog-wall')
})

it('skips a malformed followed_user row missing its followee, without crashing the list', async () => {
  stubFetch({
    '/api/feed?tab=following': {
      items: [
        feedItem({ id: 'bad', verb: 'followed_user', actor: alice }),
        feedItem({ id: 'ok', verb: 'liked_shelf', actor: bob, shelf: shelf() }),
      ],
    },
  })
  renderFeed()
  expect(await screen.findAllByRole('listitem')).toHaveLength(1)
  expect(screen.getByRole('link', { name: 'Backlog Wall' })).toBeInTheDocument()
})

it('skips a malformed shelf-verb row missing its shelf, without crashing the list', async () => {
  stubFetch({
    '/api/feed?tab=following': {
      items: [
        feedItem({ id: 'bad', verb: 'liked_shelf', actor: alice }),
        feedItem({ id: 'ok', verb: 'followed_user', actor: bob, followed_user: alice }),
      ],
    },
  })
  renderFeed()
  expect(await screen.findAllByRole('listitem')).toHaveLength(1)
  expect(screen.getByText('followed')).toBeInTheDocument()
})

it('defaults to the Following tab, selected, and refetches the You tab with tab=you on click', async () => {
  const fetchMock = stubFetch({
    '/api/feed?tab=following': { items: [feedItem({ shelf: shelf({ name: 'Backlog Wall' }) })] },
    '/api/feed?tab=you': {
      items: [feedItem({ id: 'f2', shelf: shelf({ id: 's2', name: 'Hall of Fame', slug: 'hall-of-fame' }) })],
    },
  })
  renderFeed()
  expect(screen.getByRole('tab', { name: 'Following', selected: true })).toBeInTheDocument()
  expect(screen.getByRole('tab', { name: 'You', selected: false })).toBeInTheDocument()
  await screen.findByRole('link', { name: 'Backlog Wall' })

  await userEvent.click(screen.getByRole('tab', { name: 'You' }))

  expect(await screen.findByRole('link', { name: 'Hall of Fame' })).toBeInTheDocument()
  expect(screen.queryByRole('link', { name: 'Backlog Wall' })).not.toBeInTheDocument()
  expect(screen.getByRole('tab', { name: 'You', selected: true })).toBeInTheDocument()
  expect(fetchMock.mock.calls.some((c) => String(c[0]) === '/api/feed?tab=you')).toBe(true)
})

it('shows an Explore CTA when the Following feed is empty', async () => {
  stubFetch({ '/api/feed?tab=following': { items: [] } })
  renderFeed()
  const link = await screen.findByRole('link', { name: 'Explore' })
  expect(link).toHaveAttribute('href', '/explore')
})

it('shows a plain empty state on the You tab, with no Explore CTA', async () => {
  stubFetch({
    '/api/feed?tab=following': { items: [] },
    '/api/feed?tab=you': { items: [] },
  })
  renderFeed()
  await screen.findByRole('link', { name: 'Explore' })
  await userEvent.click(screen.getByRole('tab', { name: 'You' }))
  await screen.findByText('No activity yet.')
  expect(screen.queryByRole('link', { name: 'Explore' })).not.toBeInTheDocument()
})

it('shows Load more only when next_cursor is present, and pages via the raw cursor', async () => {
  const first = { items: [feedItem({ id: 'f1', shelf: shelf({ name: 'Backlog Wall' }) })], next_cursor: 'cur1' }
  const second = {
    items: [feedItem({ id: 'f2', shelf: shelf({ id: 's2', name: 'Second Shelf', slug: 'second-shelf' }) })],
  }
  const fetchMock = stubFetch({ '/api/feed?tab=following': [first, second] })
  renderFeed()
  await screen.findByRole('link', { name: 'Backlog Wall' })
  expect(screen.getByRole('button', { name: 'Load more' })).toBeInTheDocument()

  await userEvent.click(screen.getByRole('button', { name: 'Load more' }))

  expect(await screen.findByRole('link', { name: 'Second Shelf' })).toBeInTheDocument()
  expect(screen.queryByRole('button', { name: 'Load more' })).not.toBeInTheDocument()
  expect(fetchMock.mock.calls[1][0]).toBe('/api/feed?tab=following&cursor=cur1')
})

it('shows no Load more when next_cursor is absent', async () => {
  stubFetch({ '/api/feed?tab=following': { items: [feedItem({ shelf: shelf() })] } })
  renderFeed()
  await screen.findByRole('link', { name: 'Backlog Wall' })
  expect(screen.queryByRole('button', { name: 'Load more' })).not.toBeInTheDocument()
})
