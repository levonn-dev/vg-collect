import {
  deleteProduct,
  dismissPromoteCandidate,
  fetchPromoteCandidates,
  fetchSubmissions,
  fetchUnmatchedProducts,
  promoteProduct,
  runResnapshot,
  setProductMapping,
  submitVerdict,
  triggerRefresh,
  triggerRematch,
} from './admin'
import { calledPath, jsonResponse } from '../test/fixtures'

afterEach(() => vi.unstubAllGlobals())

it('fetchUnmatchedProducts reads the worklist page at an offset', async () => {
  const fetchMock = vi.fn().mockResolvedValue(jsonResponse(200, { products: [], total_count: 0 }))
  vi.stubGlobal('fetch', fetchMock)
  await fetchUnmatchedProducts(200)
  expect(calledPath(fetchMock)).toBe('/api/admin/products/unmatched?offset=200')
})

it('setProductMapping puts the listing id', async () => {
  const fetchMock = vi.fn().mockResolvedValue(jsonResponse(200, { id: 'p1', type: 'game', name: 'CT' }))
  vi.stubGlobal('fetch', fetchMock)
  await setProductMapping('p1', 5005)
  expect(calledPath(fetchMock, 0)).toBe('/api/admin/products/p1/pricecharting')
  const req = fetchMock.mock.calls[0][0] as Request
  expect(req.method).toBe('PUT')
  expect(await req.text()).toBe(JSON.stringify({ pc_product_id: 5005 }))
})

it('setProductMapping puts null to clear', async () => {
  const fetchMock = vi.fn().mockResolvedValue(jsonResponse(200, { id: 'p1', type: 'game', name: 'CT', match_hold: true }))
  vi.stubGlobal('fetch', fetchMock)
  const p = await setProductMapping('p1', null)
  expect(await (fetchMock.mock.calls[0][0] as Request).text()).toBe(JSON.stringify({ pc_product_id: null }))
  expect(p.match_hold).toBe(true)
})

it('triggerRefresh posts the refresh trigger', async () => {
  const fetchMock = vi.fn().mockResolvedValue(jsonResponse(202, { status: 'started' }))
  vi.stubGlobal('fetch', fetchMock)
  const r = await triggerRefresh()
  expect(calledPath(fetchMock, 0)).toBe('/api/admin/refresh')
  expect(r.status).toBe('started')
})

it('triggerRematch posts the rematch trigger', async () => {
  const fetchMock = vi.fn().mockResolvedValue(jsonResponse(202, { status: 'started' }))
  vi.stubGlobal('fetch', fetchMock)
  const r = await triggerRematch()
  expect(calledPath(fetchMock, 0)).toBe('/api/admin/rematch')
  expect(r.status).toBe('started')
})

it('runResnapshot posts the resnapshot sweep', async () => {
  const fetchMock = vi.fn().mockResolvedValue(jsonResponse(200, { products_seen: 3, products_failed: 0, entries_updated: 2 }))
  vi.stubGlobal('fetch', fetchMock)
  const r = await runResnapshot()
  expect(calledPath(fetchMock, 0)).toBe('/api/admin/resnapshot')
  expect(r.entries_updated).toBe(2)
})

it('deleteProduct issues the DELETE and resolves on 204', async () => {
  const fetchMock = vi.fn().mockResolvedValue(new Response(null, { status: 204 }))
  vi.stubGlobal('fetch', fetchMock)
  await deleteProduct('p9')
  expect(calledPath(fetchMock, 0)).toBe('/api/admin/products/p9')
  expect((fetchMock.mock.calls[0][0] as Request).method).toBe('DELETE')
})

it('fetchSubmissions reads the queue at an offset', async () => {
  const fetchMock = vi.fn().mockResolvedValue(jsonResponse(200, { submissions: [], total_count: 0 }))
  vi.stubGlobal('fetch', fetchMock)
  await fetchSubmissions(200)
  expect(calledPath(fetchMock)).toBe('/api/admin/submissions?offset=200')
})

it('submitVerdict posts the verdict body', async () => {
  const fetchMock = vi.fn().mockResolvedValue(jsonResponse(200, { id: 's1', entry_id: 'e1', status: 'rejected', created_at: 'x', updated_at: 'x' }))
  vi.stubGlobal('fetch', fetchMock)
  await submitVerdict('s1', { action: 'reject', reason: 'nope' })
  expect(calledPath(fetchMock, 0)).toBe('/api/admin/submissions/s1/verdict')
  const req = fetchMock.mock.calls[0][0] as Request
  expect(req.method).toBe('POST')
  expect(await req.text()).toBe(JSON.stringify({ action: 'reject', reason: 'nope' }))
})

it('fetchPromoteCandidates filters by product when given', async () => {
  const fetchMock = vi.fn().mockResolvedValue(jsonResponse(200, { products: [], total_count: 0 }))
  vi.stubGlobal('fetch', fetchMock)
  await fetchPromoteCandidates(0, 'p1')
  expect(calledPath(fetchMock)).toBe('/api/admin/products/promote-candidates?offset=0&product_id=p1')
})

it('promoteProduct posts provider identity', async () => {
  const fetchMock = vi.fn().mockResolvedValue(jsonResponse(200, { id: 'p1', type: 'game', name: 'CT', created_at: 'x', updated_at: 'x' }))
  vi.stubGlobal('fetch', fetchMock)
  await promoteProduct('p1', { igdb_game_id: 1011, platform_igdb_id: 19 })
  expect(calledPath(fetchMock, 0)).toBe('/api/admin/products/p1/promote')
  const req = fetchMock.mock.calls[0][0] as Request
  expect(req.method).toBe('POST')
  expect(await req.text()).toBe(JSON.stringify({ igdb_game_id: 1011, platform_igdb_id: 19 }))
})

it('dismissPromoteCandidate posts the pair', async () => {
  const fetchMock = vi.fn().mockResolvedValue(new Response(null, { status: 204 }))
  vi.stubGlobal('fetch', fetchMock)
  await dismissPromoteCandidate('p1', 'igdb', 1011)
  expect(calledPath(fetchMock, 0)).toBe('/api/admin/products/p1/promote-candidates/dismiss')
  const req = fetchMock.mock.calls[0][0] as Request
  expect(req.method).toBe('POST')
  expect(await req.text()).toBe(JSON.stringify({ provider: 'igdb', provider_id: 1011 }))
})
