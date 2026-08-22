import { api, ApiError, unwrap } from './client'
import { calledPath, jsonResponse, problemResponse } from '../test/fixtures'

afterEach(() => vi.unstubAllGlobals())

it('a body-bearing call rides as JSON and unwrap parses the answer', async () => {
  const fetchMock = vi.fn().mockResolvedValue(jsonResponse(201, { id: 't1', name: 'rpg', entry_count: 0 }))
  vi.stubGlobal('fetch', fetchMock)
  const created = await unwrap(await api.POST('/api/tags', { body: { name: 'rpg' } }))
  expect(created.id).toBe('t1')
  expect(calledPath(fetchMock)).toBe('/api/tags')
  const req = fetchMock.mock.calls[0][0] as Request
  expect(req.method).toBe('POST')
  expect(req.headers.get('Content-Type')).toBe('application/json')
  expect(await req.text()).toBe(JSON.stringify({ name: 'rpg' }))
})

it('unwrap resolves undefined on 204 and the request omits the body when absent', async () => {
  const fetchMock = vi.fn().mockResolvedValue(new Response(null, { status: 204 }))
  vi.stubGlobal('fetch', fetchMock)
  const answer = api.DELETE('/api/entries/{entryId}', { params: { path: { entryId: 'e1' } } })
  await expect(answer.then(unwrap<void>)).resolves.toBeUndefined()
  expect(calledPath(fetchMock)).toBe('/api/entries/e1')
  const req = fetchMock.mock.calls[0][0] as Request
  expect(req.method).toBe('DELETE')
  expect(req.body).toBeNull()
})

it('threads the keepalive option onto the request', async () => {
  const fetchMock = vi.fn().mockResolvedValue(new Response(null, { status: 204 }))
  vi.stubGlobal('fetch', fetchMock)
  const answer = await api.DELETE('/api/comments/{commentId}', {
    params: { path: { commentId: 'c1' } },
    keepalive: true,
  })
  await expect(unwrap<void>(answer)).resolves.toBeUndefined()
  expect(calledPath(fetchMock)).toBe('/api/comments/c1')
  expect((fetchMock.mock.calls[0][0] as Request).keepalive).toBe(true)
})

it('unwrap maps problem bodies onto ApiError', async () => {
  vi.stubGlobal('fetch', vi.fn().mockResolvedValue(problemResponse(409, 'conflicting_order')))
  const err = await unwrap(
    await api.POST('/api/entries/{entryId}/reorder', { params: { path: { entryId: 'e1' } }, body: {} }),
  ).catch((e: unknown) => e)
  expect(err).toBeInstanceOf(ApiError)
  expect((err as ApiError).code).toBe('conflicting_order')
})
