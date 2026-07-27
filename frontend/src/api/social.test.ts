import {
  deleteComment, fetchExplore, fetchFeed, fetchProfilePage, fetchShelfComments, fetchShelfEntries,
  fetchShelfPage, follow, like, postComment, searchUsers, unfollow, unlike,
} from './social'

const jsonResponse = (status: number, body: unknown) =>
  new Response(JSON.stringify(body), {
    status,
    headers: { 'Content-Type': 'application/json' },
  })

afterEach(() => vi.unstubAllGlobals())

it('fetchProfilePage and fetchShelfPage hit the handle/slug routes', async () => {
  const fetchMock = vi.fn()
    .mockResolvedValueOnce(jsonResponse(200, {
      profile: {}, social_available: false, shelves: [], total_count: 0,
    }))
    .mockResolvedValueOnce(jsonResponse(200, { shelf: {}, owner: {}, social_available: false }))
  vi.stubGlobal('fetch', fetchMock)
  await fetchProfilePage('alice')
  expect(fetchMock).toHaveBeenLastCalledWith('/api/profiles/alice')
  await fetchShelfPage('alice', 'backlog')
  expect(fetchMock).toHaveBeenLastCalledWith('/api/profiles/alice/shelves/backlog')
})

it('fetchShelfEntries defaults offset to 0 and carries an explicit one', async () => {
  const emptyList = { total_count: 0, entries: [] }
  const fetchMock = vi.fn()
    .mockResolvedValueOnce(jsonResponse(200, emptyList))
    .mockResolvedValueOnce(jsonResponse(200, emptyList))
  vi.stubGlobal('fetch', fetchMock)
  await fetchShelfEntries('s1')
  expect(fetchMock).toHaveBeenLastCalledWith('/api/shelves/s1/entries?offset=0')
  await fetchShelfEntries('s1', 100)
  expect(fetchMock).toHaveBeenLastCalledWith('/api/shelves/s1/entries?offset=100')
})

it('fetchShelfComments omits the query string until a cursor is given', async () => {
  const emptyPage = { comments: [] }
  const fetchMock = vi.fn()
    .mockResolvedValueOnce(jsonResponse(200, emptyPage))
    .mockResolvedValueOnce(jsonResponse(200, emptyPage))
  vi.stubGlobal('fetch', fetchMock)
  await fetchShelfComments('s1')
  expect(fetchMock).toHaveBeenLastCalledWith('/api/shelves/s1/comments')
  await fetchShelfComments('s1', 'c1')
  expect(fetchMock).toHaveBeenLastCalledWith('/api/shelves/s1/comments?cursor=c1')
})

it('postComment posts the body and deleteComment tolerates 204', async () => {
  const created = {
    id: 'c1', shelf_id: 's1', author_id: 'u1', body: 'hi', created_at: '2026-01-01T00:00:00Z',
  }
  const fetchMock = vi.fn()
    .mockResolvedValueOnce(jsonResponse(201, created))
    .mockResolvedValueOnce(new Response(null, { status: 204 }))
  vi.stubGlobal('fetch', fetchMock)
  const comment = await postComment('s1', 'hi')
  expect(comment.id).toBe('c1')
  expect(fetchMock.mock.calls[0][0]).toBe('/api/shelves/s1/comments')
  expect((fetchMock.mock.calls[0][1] as RequestInit).body).toBe(JSON.stringify({ body: 'hi' }))
  await expect(deleteComment('c1')).resolves.toBeUndefined()
  expect(fetchMock).toHaveBeenLastCalledWith('/api/comments/c1', { method: 'DELETE' })
})

it('follow/unfollow and like/unlike PUT and DELETE with no body', async () => {
  const fetchMock = vi.fn().mockResolvedValue(new Response(null, { status: 204 }))
  vi.stubGlobal('fetch', fetchMock)
  await follow('u1')
  expect(fetchMock).toHaveBeenLastCalledWith('/api/social/follows/u1', { method: 'PUT' })
  await unfollow('u1')
  expect(fetchMock).toHaveBeenLastCalledWith('/api/social/follows/u1', { method: 'DELETE' })
  await like('s1')
  expect(fetchMock).toHaveBeenLastCalledWith('/api/social/likes/s1', { method: 'PUT' })
  await unlike('s1')
  expect(fetchMock).toHaveBeenLastCalledWith('/api/social/likes/s1', { method: 'DELETE' })
})

it('fetchFeed appends cursor only when present', async () => {
  const emptyPage = { items: [] }
  const fetchMock = vi.fn()
    .mockResolvedValueOnce(jsonResponse(200, emptyPage))
    .mockResolvedValueOnce(jsonResponse(200, emptyPage))
  vi.stubGlobal('fetch', fetchMock)
  await fetchFeed('following')
  expect(fetchMock).toHaveBeenLastCalledWith('/api/feed?tab=following')
  await fetchFeed('you', 'cur1')
  expect(fetchMock).toHaveBeenLastCalledWith('/api/feed?tab=you&cursor=cur1')
})

it('fetchExplore carries sort and offset', async () => {
  const emptyPage = { shelves: [] }
  const fetchMock = vi.fn()
    .mockResolvedValueOnce(jsonResponse(200, emptyPage))
    .mockResolvedValueOnce(jsonResponse(200, emptyPage))
  vi.stubGlobal('fetch', fetchMock)
  await fetchExplore('top')
  expect(fetchMock).toHaveBeenLastCalledWith('/api/explore?sort=top&offset=0')
  await fetchExplore('recent', 40)
  expect(fetchMock).toHaveBeenLastCalledWith('/api/explore?sort=recent&offset=40')
})

it('searchUsers unwraps the profiles envelope', async () => {
  const profile = { user_id: 'u1', handle: 'alice', profile_visibility: 'listed' }
  const fetchMock = vi.fn().mockResolvedValue(jsonResponse(200, { profiles: [profile] }))
  vi.stubGlobal('fetch', fetchMock)
  const results = await searchUsers('ali')
  expect(results).toEqual([profile])
  expect(fetchMock).toHaveBeenCalledWith('/api/search/users?q=ali')
})
