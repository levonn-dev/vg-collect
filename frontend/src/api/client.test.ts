import { ApiError, fetchMe, fetchProviders, logout } from './client'

const jsonResponse = (status: number, body: unknown) =>
  new Response(JSON.stringify(body), {
    status,
    headers: { 'Content-Type': 'application/json' },
  })

afterEach(() => vi.unstubAllGlobals())

it('fetchMe returns the profile', async () => {
  vi.stubGlobal('fetch', vi.fn().mockResolvedValue(jsonResponse(200, {
    id: 'u1', email: 'a@example.test', display_name: 'alice', roles: ['user'],
  })))
  const me = await fetchMe()
  expect(me.display_name).toBe('alice')
})

it('maps problem+json onto ApiError', async () => {
  vi.stubGlobal('fetch', vi.fn().mockResolvedValue(jsonResponse(401, {
    type: 'about:blank', title: 'Unauthorized', status: 401, code: 'unauthenticated',
  })))
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
