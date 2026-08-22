import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { MemoryRouter } from 'react-router'
import type { Comment, ProfileCard } from '../../api/social'
import { jsonResponse, meFixture, requestPath } from '../../test/fixtures'
import { renderWithI18n } from '../../test/i18n'
import CommentList from './CommentList'

// Same route-map idiom as Profile.test/Explore.test: fetch is
// dispatched by matching prefix, and any URL nothing stubbed fails
// the test in afterEach. A route's value may be a plain body (always
// 200), a Response (explicit status), or an array of either consumed
// in call order (the load-more test's second page).
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
  const missed = unstubbed
  unstubbed = []
  expect(missed).toEqual([])
})

function comment(overrides: Partial<Comment> = {}): Comment {
  return {
    id: 'c1', shelf_id: 's1', author_id: 'other1', body: 'Nice shelf!',
    created_at: new Date().toISOString(),
    ...overrides,
  }
}

function renderList(ownerId = 'owner1') {
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } })
  return renderWithI18n(
    <QueryClientProvider client={qc}>
      <MemoryRouter>
        <CommentList shelfId="s1" ownerId={ownerId} />
      </MemoryRouter>
    </QueryClientProvider>,
  )
}

it('shows a loading state before comments resolve', () => {
  vi.stubGlobal('fetch', vi.fn().mockImplementation(() => new Promise(() => {})))
  renderList()
  expect(screen.getByText(/loading comments/i)).toBeInTheDocument()
})

it('shows an error state when comments fail to load', async () => {
  stubFetch({ '/api/me': meFixture(), '/api/shelves/s1/comments': jsonResponse(500, {}) })
  renderList()
  expect(await screen.findByRole('alert')).toHaveTextContent(/cannot be loaded/i)
})

it('shows an empty state with no comments', async () => {
  stubFetch({ '/api/me': meFixture(), '/api/shelves/s1/comments': { comments: [] } })
  renderList()
  expect(await screen.findByText('No comments yet.')).toBeInTheDocument()
})

it('renders each live comment with its body, and the neutral placeholder identity when hydration has not attached an author (fail-open)', async () => {
  stubFetch({
    '/api/me': meFixture({ id: 'visitor', handle: 'visitor' }),
    // author_id is present but author is not: the shape a card-fetch
    // fail-open leaves behind (see composeCommentsPage in the bff).
    '/api/shelves/s1/comments': { comments: [comment({ body: 'Great collection' })] },
  })
  renderList()
  expect(await screen.findByText('Great collection')).toBeInTheDocument()
  expect(screen.getByText('just now')).toBeInTheDocument()
  expect(screen.getByText('Member')).toBeInTheDocument()
  // Regex, not the old bare 'Delete'/'Remove': the accessible name now
  // carries the comment body too, and an exact-string match here would
  // find nothing regardless of whether a button wrongly rendered,
  // silently defeating this absence check.
  expect(screen.queryByRole('button', { name: /^Delete/ })).not.toBeInTheDocument()
  expect(screen.queryByRole('button', { name: /^Remove/ })).not.toBeInTheDocument()
})

it('renders a hydrated authors real chip, linking to their public profile', async () => {
  const author: ProfileCard = { user_id: 'u2', handle: 'Bob_Prime', profile_visibility: 'listed' }
  stubFetch({
    '/api/me': meFixture({ id: 'visitor', handle: 'visitor' }),
    '/api/shelves/s1/comments': {
      comments: [comment({ author_id: 'u2', body: 'Great collection', author })],
    },
  })
  renderList()
  await screen.findByText('Great collection')
  const link = screen.getByRole('link', { name: '@Bob_Prime' })
  expect(link).toHaveAttribute('href', '/u/Bob_Prime')
  expect(screen.queryByText('Member')).not.toBeInTheDocument()
})

it('shows a Deleted user placeholder for a purge-anonymized comment', async () => {
  stubFetch({
    '/api/me': meFixture({ id: 'visitor', handle: 'visitor' }),
    '/api/shelves/s1/comments': {
      comments: [
        comment({
          // The wire shape a purge-anonymized comment sends: author_id
          // itself comes back null (required+nullable in the schema).
          author_id: null,
          body: 'Anonymized comment',
        }),
      ],
    },
  })
  renderList()
  await screen.findByText('Anonymized comment')
  expect(screen.getByText('Deleted user')).toBeInTheDocument()
  expect(screen.queryByText('Member')).not.toBeInTheDocument()
})

it('renders the viewer own real identity chip and a Delete affordance for their own comment', async () => {
  const author: ProfileCard = { user_id: 'u1', handle: 'me', profile_visibility: 'listed' }
  stubFetch({
    '/api/me': meFixture({ id: 'u1', handle: 'me' }),
    '/api/shelves/s1/comments': {
      comments: [comment({ author_id: 'u1', body: 'My own comment', author })],
    },
  })
  renderList('owner1')
  await screen.findByText('My own comment')
  expect(screen.getByRole('link', { name: '@me' })).toBeInTheDocument()
  expect(screen.queryByText('Member')).not.toBeInTheDocument()
  expect(screen.getByRole('button', { name: 'Delete your comment: My own comment' })).toBeInTheDocument()
  expect(screen.queryByRole('button', { name: /^Remove/ })).not.toBeInTheDocument()
})

it('truncates a comment body over 30 chars to 30 chars plus an ellipsis in the Delete accessible name', async () => {
  const longBody = 'This comment body runs well past the thirty character truncation limit.'
  const author: ProfileCard = { user_id: 'u1', handle: 'me', profile_visibility: 'listed' }
  stubFetch({
    '/api/me': meFixture({ id: 'u1', handle: 'me' }),
    '/api/shelves/s1/comments': {
      comments: [comment({ author_id: 'u1', body: longBody, author })],
    },
  })
  renderList('owner1')
  await screen.findByText(longBody)
  const truncated = `${longBody.slice(0, 30)}...`
  expect(screen.getByRole('button', { name: `Delete your comment: ${truncated}` })).toBeInTheDocument()
})

it('self-delete hides the row behind an undo toast with no immediate DELETE, and Undo restores it', async () => {
  const fetchMock = stubFetch({
    '/api/me': meFixture({ id: 'u1', handle: 'me' }),
    '/api/shelves/s1/comments': { comments: [comment({ author_id: 'u1', body: 'My own comment' })] },
  })
  renderList()
  await screen.findByText('My own comment')
  await userEvent.click(screen.getByRole('button', { name: 'Delete your comment: My own comment' }))

  expect(screen.queryByText('My own comment')).not.toBeInTheDocument()
  expect(screen.getByRole('status')).toHaveTextContent('Comment deleted - Undo')
  expect(fetchMock.mock.calls.some(([url]) => requestPath(url) === '/api/comments/c1')).toBe(false)

  await userEvent.click(screen.getByRole('button', { name: 'Undo' }))
  expect(await screen.findByText('My own comment')).toBeInTheDocument()
  expect(screen.queryByRole('status')).not.toBeInTheDocument()
  expect(fetchMock.mock.calls.some(([url]) => requestPath(url) === '/api/comments/c1')).toBe(false)
})

it('lets the shelf owner remove someone elses comment immediately, after confirming', async () => {
  const confirmSpy = vi.spyOn(window, 'confirm').mockReturnValue(true)
  const fetchMock = stubFetch({
    '/api/me': meFixture({ id: 'owner1', handle: 'owner' }),
    '/api/shelves/s1/comments': { comments: [comment({ author_id: 'other1', body: 'Spam' })] },
    '/api/comments/c1': new Response(null, { status: 204 }),
  })
  renderList('owner1')
  await screen.findByText('Spam')
  await userEvent.click(screen.getByRole('button', { name: 'Remove comment: Spam' }))

  expect(confirmSpy).toHaveBeenCalledWith('Remove this comment? The author will not be able to restore it.')
  await waitFor(() => expect(fetchMock.mock.calls.some(
    (c) => requestPath(c[0]) === '/api/comments/c1' && (c[0] as Request).method === 'DELETE',
  )).toBe(true))
  confirmSpy.mockRestore()
})

it('does nothing when the owner cancels the confirm', async () => {
  const confirmSpy = vi.spyOn(window, 'confirm').mockReturnValue(false)
  const fetchMock = stubFetch({
    '/api/me': meFixture({ id: 'owner1', handle: 'owner' }),
    '/api/shelves/s1/comments': { comments: [comment({ author_id: 'other1', body: 'Spam' })] },
  })
  renderList('owner1')
  await screen.findByText('Spam')
  await userEvent.click(screen.getByRole('button', { name: 'Remove comment: Spam' }))

  expect(confirmSpy).toHaveBeenCalled()
  expect(fetchMock.mock.calls.some(([url]) => requestPath(url) === '/api/comments/c1')).toBe(false)
  expect(screen.getByText('Spam')).toBeInTheDocument()
  confirmSpy.mockRestore()
})

it('loads the next comment page via Load more and appends it', async () => {
  const first = { comments: [comment({ id: 'c1', body: 'First' })], next_cursor: 'cur1' }
  const second = { comments: [comment({ id: 'c2', body: 'Second' })] }
  const fetchMock = stubFetch({ '/api/me': meFixture(), '/api/shelves/s1/comments': [first, second] })
  renderList()
  await screen.findByText('First')
  expect(screen.queryByText('Second')).not.toBeInTheDocument()
  await userEvent.click(screen.getByRole('button', { name: 'Load more' }))
  expect(await screen.findByText('Second')).toBeInTheDocument()
  expect(fetchMock.mock.calls.map((c) => requestPath(c[0]))).toContain('/api/shelves/s1/comments?cursor=cur1')
})
