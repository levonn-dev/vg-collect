import {
  deleteComment, fetchExplore, fetchFeed, fetchProfilePage, fetchShelfComments, fetchShelfEntries,
  fetchShelfPage, follow, like, postComment, searchUsers, unfollow, unlike,
} from './social'
import { calledPath, jsonResponse } from '../test/fixtures'

afterEach(() => vi.unstubAllGlobals())

it('fetchProfilePage and fetchShelfPage hit the handle/slug routes', async () => {
  const fetchMock = vi.fn()
    .mockResolvedValueOnce(jsonResponse(200, {
      profile: {}, social_available: false, shelves: [], total_count: 0,
    }))
    .mockResolvedValueOnce(jsonResponse(200, { shelf: {}, owner: {}, social_available: false }))
  vi.stubGlobal('fetch', fetchMock)
  await fetchProfilePage('alice')
  expect(calledPath(fetchMock)).toBe('/api/profiles/alice')
  await fetchShelfPage('alice', 'backlog')
  expect(calledPath(fetchMock)).toBe('/api/profiles/alice/shelves/backlog')
})

it('fetchShelfEntries defaults offset to 0 and carries an explicit one', async () => {
  const emptyList = { total_count: 0, entries: [] }
  const fetchMock = vi.fn()
    .mockResolvedValueOnce(jsonResponse(200, emptyList))
    .mockResolvedValueOnce(jsonResponse(200, emptyList))
  vi.stubGlobal('fetch', fetchMock)
  await fetchShelfEntries('s1')
  expect(calledPath(fetchMock)).toBe('/api/shelves/s1/entries?offset=0')
  await fetchShelfEntries('s1', 100)
  expect(calledPath(fetchMock)).toBe('/api/shelves/s1/entries?offset=100')
})

it('fetchShelfComments omits the query string until a cursor is given', async () => {
  const emptyPage = { comments: [] }
  const fetchMock = vi.fn()
    .mockResolvedValueOnce(jsonResponse(200, emptyPage))
    .mockResolvedValueOnce(jsonResponse(200, emptyPage))
  vi.stubGlobal('fetch', fetchMock)
  await fetchShelfComments('s1')
  expect(calledPath(fetchMock)).toBe('/api/shelves/s1/comments')
  await fetchShelfComments('s1', 'c1')
  expect(calledPath(fetchMock)).toBe('/api/shelves/s1/comments?cursor=c1')
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
  expect(calledPath(fetchMock, 0)).toBe('/api/shelves/s1/comments')
  expect(await (fetchMock.mock.calls[0][0] as Request).text()).toBe(JSON.stringify({ body: 'hi' }))
  await expect(deleteComment('c1')).resolves.toBeUndefined()
  expect(calledPath(fetchMock)).toBe('/api/comments/c1')
  expect((fetchMock.mock.calls[1][0] as Request).method).toBe('DELETE')
})

it('follow/unfollow and like/unlike PUT and DELETE with no body', async () => {
  const fetchMock = vi.fn().mockResolvedValue(new Response(null, { status: 204 }))
  vi.stubGlobal('fetch', fetchMock)
  await follow('u1')
  expect(calledPath(fetchMock)).toBe('/api/social/follows/u1')
  expect((fetchMock.mock.calls[0][0] as Request).method).toBe('PUT')
  await unfollow('u1')
  expect(calledPath(fetchMock)).toBe('/api/social/follows/u1')
  expect((fetchMock.mock.calls[1][0] as Request).method).toBe('DELETE')
  await like('s1')
  expect(calledPath(fetchMock)).toBe('/api/social/likes/s1')
  expect((fetchMock.mock.calls[2][0] as Request).method).toBe('PUT')
  await unlike('s1')
  expect(calledPath(fetchMock)).toBe('/api/social/likes/s1')
  expect((fetchMock.mock.calls[3][0] as Request).method).toBe('DELETE')
})

it('fetchFeed appends cursor only when present', async () => {
  const emptyPage = { items: [] }
  const fetchMock = vi.fn()
    .mockResolvedValueOnce(jsonResponse(200, emptyPage))
    .mockResolvedValueOnce(jsonResponse(200, emptyPage))
  vi.stubGlobal('fetch', fetchMock)
  await fetchFeed('following')
  expect(calledPath(fetchMock)).toBe('/api/feed?tab=following')
  await fetchFeed('you', 'cur1')
  expect(calledPath(fetchMock)).toBe('/api/feed?tab=you&cursor=cur1')
})

it('fetchExplore carries sort and offset', async () => {
  const emptyPage = { shelves: [] }
  const fetchMock = vi.fn()
    .mockResolvedValueOnce(jsonResponse(200, emptyPage))
    .mockResolvedValueOnce(jsonResponse(200, emptyPage))
  vi.stubGlobal('fetch', fetchMock)
  await fetchExplore('top')
  expect(calledPath(fetchMock)).toBe('/api/explore?sort=top&offset=0')
  await fetchExplore('recent', 40)
  expect(calledPath(fetchMock)).toBe('/api/explore?sort=recent&offset=40')
})

it('searchUsers unwraps the profiles envelope', async () => {
  const profile = { user_id: 'u1', handle: 'alice', profile_visibility: 'listed' }
  const fetchMock = vi.fn().mockResolvedValue(jsonResponse(200, { profiles: [profile] }))
  vi.stubGlobal('fetch', fetchMock)
  const results = await searchUsers('ali')
  expect(results).toEqual([profile])
  expect(calledPath(fetchMock)).toBe('/api/search/users?q=ali')
})
