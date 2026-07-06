import { ApiError, fetchMe, fetchProviders, logout, sendJSON } from './client'

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

it('sendJSON maps problem bodies onto ApiError', async () => {
  vi.stubGlobal('fetch', vi.fn().mockResolvedValue(jsonResponse(409, {
    type: 'about:blank', title: 'Conflict', status: 409, code: 'conflicting_order',
  })))
  const err = await sendJSON('POST', '/api/entries/e1/reorder', {}).catch((e: unknown) => e)
  expect(err).toBeInstanceOf(ApiError)
  expect((err as ApiError).code).toBe('conflicting_order')
})
