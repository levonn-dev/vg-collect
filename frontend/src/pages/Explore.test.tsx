import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { act, fireEvent, render, screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { MemoryRouter } from 'react-router'
import type { ProfileCard, ShelfCard as ShelfCardData } from '../api/social'
import { jsonResponse } from '../test/fixtures'
import Explore from './Explore'

// Mirrors Explore's own (unexported) debounce window.
const SEARCH_DEBOUNCE_MS = 300

function renderExplore() {
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } })
  return render(
    <QueryClientProvider client={qc}>
      <MemoryRouter>
        <Explore />
      </MemoryRouter>
    </QueryClientProvider>,
  )
}

// Fetch is routed per endpoint (first matching prefix wins) so each
// test declares exactly the calls it expects; a URL nothing stubbed
// is recorded and fails the test in afterEach (Admin.test's idiom). A
// route's value may be a plain body (always 200), a Response (explicit
// status), or an array of either consumed in call order - the last
// entry repeats once exhausted - which is what the load-more test
// needs for a second, different page.
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

const alice: ProfileCard = { user_id: 'u1', handle: 'Alice_Prime', profile_visibility: 'listed' }

function shelf(id: string, name: string): ShelfCardData {
  return {
    id, name, slug: name.toLowerCase().replace(/\s+/g, '-'),
    owner: alice, entry_count: 3, cover_urls: [],
  }
}

it('renders recent shelves from the stubbed recent-sort page', async () => {
  stubFetch({ '/api/explore?sort=recent': { shelves: [shelf('s1', 'Backlog Wall')] } })
  renderExplore()
  expect(await screen.findByRole('link', { name: 'Backlog Wall' })).toBeInTheDocument()
  expect(screen.getByRole('tab', { name: 'Recent', selected: true })).toBeInTheDocument()
})

it('shows a loading state before the active tab resolves', () => {
  vi.stubGlobal('fetch', vi.fn().mockImplementation(() => new Promise(() => {})))
  renderExplore()
  expect(screen.getByText(/loading shelves/i)).toBeInTheDocument()
})

it('shows an error state when the shelf page fails to load', async () => {
  stubFetch({ '/api/explore?sort=recent': jsonResponse(500, {}) })
  renderExplore()
  expect(await screen.findByRole('alert')).toHaveTextContent(/cannot be loaded/i)
})

it('explains an empty recent page', async () => {
  stubFetch({ '/api/explore?sort=recent': { shelves: [] } })
  renderExplore()
  expect(await screen.findByText('No shared shelves yet.')).toBeInTheDocument()
})

it('caps the search input to the backend query length limit', async () => {
  stubFetch({ '/api/explore?sort=recent': { shelves: [] } })
  renderExplore()
  const box = screen.getByRole('searchbox', { name: /search for people/i })
  expect(box).toHaveAttribute('maxLength', '64')
  await screen.findByText('No shared shelves yet.')
})

it('swaps to the top-sort query, and its data, when the Top tab is selected', async () => {
  stubFetch({
    '/api/explore?sort=recent': { shelves: [shelf('s1', 'Backlog Wall')] },
    '/api/explore?sort=top': { shelves: [shelf('s2', 'Hall of Fame')] },
  })
  renderExplore()
  await screen.findByRole('link', { name: 'Backlog Wall' })
  await userEvent.click(screen.getByRole('tab', { name: 'Top' }))
  expect(await screen.findByRole('link', { name: 'Hall of Fame' })).toBeInTheDocument()
  expect(screen.queryByRole('link', { name: 'Backlog Wall' })).not.toBeInTheDocument()
})

it('loads the next recent page via Load more, using the server-computed next_offset', async () => {
  // next_offset (30) deliberately differs from shelves.length (24) -
  // the bff's collection-space offset skips rows gated out server-side,
  // so a client-side loaded-count tally would resume from the wrong
  // spot. This proves the offset comes from the response, not a count.
  const first = { shelves: Array.from({ length: 24 }, (_, i) => shelf(`s${i}`, `Shelf ${i}`)), next_offset: 30 }
  const second = { shelves: [shelf('s24', 'Shelf 24')] }
  const fetchMock = stubFetch({ '/api/explore?sort=recent': [first, second] })
  renderExplore()
  await screen.findByRole('link', { name: 'Shelf 0' })
  expect(screen.queryByRole('link', { name: 'Shelf 24' })).not.toBeInTheDocument()
  await userEvent.click(screen.getByRole('button', { name: 'Load more' }))
  expect(await screen.findByRole('link', { name: 'Shelf 24' })).toBeInTheDocument()
  expect(fetchMock.mock.calls[1][0]).toBe('/api/explore?sort=recent&offset=30')
  // The second page carries no next_offset - the stream is exhausted,
  // so Load more must not render.
  expect(screen.queryByRole('button', { name: 'Load more' })).not.toBeInTheDocument()
})

// Faking only setTimeout/clearTimeout keeps the fake clock scoped to
// the debounce's own timer. React's async act() (which findBy*/
// waitFor wrap internally) needs real timers to flush even once the
// DOM is already correct, so every test below switches back to real
// timers immediately after the advance - before any findBy* call,
// never mid-wait (waitFor forbids that switch, and for good reason:
// half-drained timers make for a confusing failure).
function advanceDebounce() {
  act(() => {
    vi.advanceTimersByTime(SEARCH_DEBOUNCE_MS)
  })
  vi.useRealTimers()
}

it('debounces the search box, firing /api/search/users only once idle, and renders UserChips', async () => {
  vi.useFakeTimers({ toFake: ['setTimeout', 'clearTimeout'] })
  stubFetch({
    '/api/explore?sort=recent': { shelves: [] },
    '/api/search/users': { profiles: [alice] },
  })
  renderExplore()
  const box = screen.getByRole('searchbox', { name: /search for people/i })
  fireEvent.change(box, { target: { value: 'alice' } })
  expect(screen.queryByRole('link', { name: '@Alice_Prime' })).not.toBeInTheDocument()
  advanceDebounce()
  expect(await screen.findByRole('link', { name: '@Alice_Prime' })).toBeInTheDocument()
})

it('shows a Searching indicator while the debounced query is in flight, replaced by results once it resolves', async () => {
  vi.useFakeTimers({ toFake: ['setTimeout', 'clearTimeout'] })
  // A held promise (Explore.test's stubFetch resolves every route
  // immediately, which can never model an in-flight fetch) - same
  // captured-resolve idiom as CommentList.test's in-flight regression.
  let resolveSearch!: (res: Response) => void
  vi.stubGlobal('fetch', vi.fn().mockImplementation((url: string) => {
    if (String(url).startsWith('/api/search/users')) {
      return new Promise<Response>((resolve) => { resolveSearch = resolve })
    }
    return Promise.resolve(jsonResponse(200, { shelves: [] }))
  }))
  renderExplore()
  const box = screen.getByRole('searchbox', { name: /search for people/i })
  fireEvent.change(box, { target: { value: 'alice' } })
  advanceDebounce()

  expect(await screen.findByText('Searching...')).toBeInTheDocument()
  expect(screen.queryByRole('link', { name: '@Alice_Prime' })).not.toBeInTheDocument()

  await act(async () => {
    resolveSearch(jsonResponse(200, { profiles: [alice] }))
    await Promise.resolve()
    await Promise.resolve()
  })
  expect(await screen.findByRole('link', { name: '@Alice_Prime' })).toBeInTheDocument()
  expect(screen.queryByText('Searching...')).not.toBeInTheDocument()
})

it('shows an error state when the user search fails', async () => {
  vi.useFakeTimers({ toFake: ['setTimeout', 'clearTimeout'] })
  stubFetch({
    '/api/explore?sort=recent': { shelves: [] },
    '/api/search/users': jsonResponse(500, {}),
  })
  renderExplore()
  const box = screen.getByRole('searchbox', { name: /search for people/i })
  fireEvent.change(box, { target: { value: 'alice' } })
  advanceDebounce()
  expect(await screen.findByRole('alert')).toHaveTextContent(/search is not working right now/i)
})

it('does not search under two characters, even once idle', async () => {
  vi.useFakeTimers({ toFake: ['setTimeout', 'clearTimeout'] })
  stubFetch({ '/api/explore?sort=recent': { shelves: [] } })
  renderExplore()
  const box = screen.getByRole('searchbox', { name: /search for people/i })
  fireEvent.change(box, { target: { value: 'a' } })
  advanceDebounce()
  await screen.findByText('No shared shelves yet.')
  expect(screen.queryByText(/no people found/i)).not.toBeInTheDocument()
})

it('reports no people found once a settled search comes back empty', async () => {
  vi.useFakeTimers({ toFake: ['setTimeout', 'clearTimeout'] })
  stubFetch({
    '/api/explore?sort=recent': { shelves: [] },
    '/api/search/users': { profiles: [] },
  })
  renderExplore()
  const box = screen.getByRole('searchbox', { name: /search for people/i })
  fireEvent.change(box, { target: { value: 'zzz' } })
  advanceDebounce()
  expect(await screen.findByText('No people found for "zzz".')).toBeInTheDocument()
})
