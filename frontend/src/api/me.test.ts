import { ApiError } from './client'
import { fetchMe, fetchProviders, logout, updateMe, fetchIdentities, unlinkIdentity, deleteAccount } from './me'
import { calledPath, jsonResponse, problemResponse } from '../test/fixtures'

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
  expect(calledPath(fetchMock)).toBe('/api/auth/logout')
  expect((fetchMock.mock.calls[0][0] as Request).method).toBe('POST')
})

it('updateMe PATCHes and returns the profile', async () => {
  const fetchStub = vi.fn().mockResolvedValue(jsonResponse(200, {
    id: 'u1', email: 'a@example.test', handle: 'Neo', roles: ['user'],
  }))
  vi.stubGlobal('fetch', fetchStub)
  const me = await updateMe({ handle: 'Neo' })
  expect(me.handle).toBe('Neo')
  expect(calledPath(fetchStub)).toBe('/api/me')
  expect((fetchStub.mock.calls[0][0] as Request).method).toBe('PATCH')
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
  expect(calledPath(fetchStub)).toBe('/api/me/identities/i1')
  expect((fetchStub.mock.calls[0][0] as Request).method).toBe('DELETE')
})

it('deleteAccount DELETEs /api/me', async () => {
  const fetchStub = vi.fn().mockResolvedValue(new Response(null, { status: 204 }))
  vi.stubGlobal('fetch', fetchStub)
  await expect(deleteAccount()).resolves.toBeUndefined()
  expect(calledPath(fetchStub)).toBe('/api/me')
  expect((fetchStub.mock.calls[0][0] as Request).method).toBe('DELETE')
})
