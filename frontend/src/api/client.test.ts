import { ApiError, fetchMe, fetchProviders, logout, sendJSON, updateMe, fetchIdentities, unlinkIdentity, deleteAccount } from './client'
import { jsonResponse, problemResponse } from '../test/fixtures'

afterEach(() => vi.unstubAllGlobals())

it('fetchMe returns the profile', async () => {
  vi.stubGlobal('fetch', vi.fn().mockResolvedValue(jsonResponse(200, {
    id: 'u1', email: 'a@example.test', handle: 'alice', roles: ['user'],
  })))
  const me = await fetchMe()
  expect(me.handle).toBe('alice')
})

it('maps problem+json onto ApiError', async () => {
  vi.stubGlobal('fetch', vi.fn().mockResolvedValue(problemResponse(401, 'unauthenticated')))
  const err = await fetchMe().catch((e: unknown) => e)
  expect(err).toBeInstanceOf(ApiError)
  expect((err as ApiError).status).toBe(401)
  expect((err as ApiError).code).toBe('unauthenticated')
})

it('survives non-JSON error bodies', async () => {
  vi.stubGlobal('fetch', vi.fn().mockResolvedValue(new Response('boom', { status: 502 })))
  const err = await fetchProviders().catch((e: unknown) => e)
  expect((err as ApiError).status).toBe(502)
})

it('fetchProviders unwraps the list', async () => {
  vi.stubGlobal('fetch', vi.fn().mockResolvedValue(jsonResponse(200, { providers: ['google', 'dev'] })))
  expect(await fetchProviders()).toEqual(['google', 'dev'])
})

it('logout posts and tolerates 204', async () => {
  const fetchMock = vi.fn().mockResolvedValue(new Response(null, { status: 204 }))
  vi.stubGlobal('fetch', fetchMock)
  await logout()
  expect(fetchMock).toHaveBeenCalledWith('/api/auth/logout', { method: 'POST' })
})

it('sendJSON posts a JSON body and parses the answer', async () => {
  const fetchMock = vi.fn().mockResolvedValue(jsonResponse(201, { id: 'e1' }))
  vi.stubGlobal('fetch', fetchMock)
  const created = await sendJSON<{ id: string }>('POST', '/api/entries', { region: 'ntsc_u' })
  expect(created.id).toBe('e1')
  expect(fetchMock).toHaveBeenCalledWith('/api/entries', {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ region: 'ntsc_u' }),
  })
})

it('sendJSON resolves undefined on 204 and omits the body when absent', async () => {
  const fetchMock = vi.fn().mockResolvedValue(new Response(null, { status: 204 }))
  vi.stubGlobal('fetch', fetchMock)
  await expect(sendJSON<void>('DELETE', '/api/entries/e1')).resolves.toBeUndefined()
  expect(fetchMock).toHaveBeenCalledWith('/api/entries/e1', { method: 'DELETE' })
})

it('sendJSON threads the keepalive option into the fetch init', async () => {
  const fetchMock = vi.fn().mockResolvedValue(new Response(null, { status: 204 }))
  vi.stubGlobal('fetch', fetchMock)
  await expect(sendJSON<void>('DELETE', '/api/comments/c1', undefined, { keepalive: true })).resolves.toBeUndefined()
  expect(fetchMock).toHaveBeenCalledWith('/api/comments/c1', { method: 'DELETE', keepalive: true })
})

it('sendJSON maps problem bodies onto ApiError', async () => {
  vi.stubGlobal('fetch', vi.fn().mockResolvedValue(problemResponse(409, 'conflicting_order')))
  const err = await sendJSON('POST', '/api/entries/e1/reorder', {}).catch((e: unknown) => e)
  expect(err).toBeInstanceOf(ApiError)
  expect((err as ApiError).code).toBe('conflicting_order')
})

it('updateMe PATCHes and returns the profile', async () => {
  const fetchStub = vi.fn().mockResolvedValue(jsonResponse(200, {
    id: 'u1', email: 'a@example.test', handle: 'Neo', roles: ['user'],
  }))
  vi.stubGlobal('fetch', fetchStub)
  const me = await updateMe({ handle: 'Neo' })
  expect(me.handle).toBe('Neo')
  expect(fetchStub).toHaveBeenCalledWith('/api/me', expect.objectContaining({ method: 'PATCH' }))
})

it('fetchIdentities unwraps the list', async () => {
  vi.stubGlobal('fetch', vi.fn().mockResolvedValue(jsonResponse(200, {
    identities: [{ id: 'i1', provider: 'dev', email: 'a@example.com', created_at: '2026-01-01T00:00:00Z' }],
  })))
  const ids = await fetchIdentities()
  expect(ids).toHaveLength(1)
  expect(ids[0].provider).toBe('dev')
})

it('unlinkIdentity DELETEs and tolerates 204', async () => {
  const fetchStub = vi.fn().mockResolvedValue(new Response(null, { status: 204 }))
  vi.stubGlobal('fetch', fetchStub)
  await expect(unlinkIdentity('i1')).resolves.toBeUndefined()
  expect(fetchStub).toHaveBeenCalledWith('/api/me/identities/i1', expect.objectContaining({ method: 'DELETE' }))
})

it('deleteAccount DELETEs /api/me', async () => {
  const fetchStub = vi.fn().mockResolvedValue(new Response(null, { status: 204 }))
  vi.stubGlobal('fetch', fetchStub)
  await expect(deleteAccount()).resolves.toBeUndefined()
  expect(fetchStub).toHaveBeenCalledWith('/api/me', expect.objectContaining({ method: 'DELETE' }))
})
