import { deleteProduct, fetchUnmatchedProducts, setProductMapping, triggerRefresh } from './admin'

const jsonResponse = (status: number, body: unknown) =>
  new Response(JSON.stringify(body), {
    status,
    headers: { 'Content-Type': 'application/json' },
  })

afterEach(() => vi.unstubAllGlobals())

it('fetchUnmatchedProducts reads the worklist page at an offset', async () => {
  const fetchMock = vi.fn().mockResolvedValue(jsonResponse(200, { products: [], total_count: 0 }))
  vi.stubGlobal('fetch', fetchMock)
  await fetchUnmatchedProducts(200)
  expect(fetchMock).toHaveBeenCalledWith('/api/admin/products/unmatched?offset=200')
})

it('setProductMapping puts the listing id', async () => {
  const fetchMock = vi.fn().mockResolvedValue(jsonResponse(200, { id: 'p1', type: 'game', name: 'CT' }))
  vi.stubGlobal('fetch', fetchMock)
  await setProductMapping('p1', 5005)
  expect(fetchMock.mock.calls[0][0]).toBe('/api/admin/products/p1/pricecharting')
  expect(fetchMock.mock.calls[0][1]).toMatchObject({
    method: 'PUT',
    body: JSON.stringify({ pc_product_id: 5005 }),
  })
})

it('setProductMapping puts null to clear', async () => {
  const fetchMock = vi.fn().mockResolvedValue(jsonResponse(200, { id: 'p1', type: 'game', name: 'CT', match_hold: true }))
  vi.stubGlobal('fetch', fetchMock)
  const p = await setProductMapping('p1', null)
  expect(fetchMock.mock.calls[0][1]).toMatchObject({ body: JSON.stringify({ pc_product_id: null }) })
  expect(p.match_hold).toBe(true)
})

it('triggerRefresh posts the walk trigger', async () => {
  const fetchMock = vi.fn().mockResolvedValue(jsonResponse(202, { status: 'started' }))
  vi.stubGlobal('fetch', fetchMock)
  const r = await triggerRefresh()
  expect(fetchMock.mock.calls[0][0]).toBe('/api/admin/refresh')
  expect(r.status).toBe('started')
})

it('deleteProduct issues the DELETE and resolves on 204', async () => {
  const fetchMock = vi.fn().mockResolvedValue(new Response(null, { status: 204 }))
  vi.stubGlobal('fetch', fetchMock)
  await deleteProduct('p9')
  expect(fetchMock.mock.calls[0][0]).toBe('/api/admin/products/p9')
  expect(fetchMock.mock.calls[0][1]).toMatchObject({ method: 'DELETE' })
})
