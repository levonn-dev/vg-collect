import { fetchRecommendations, resolveProduct, searchCatalog } from './catalog'

const jsonResponse = (status: number, body: unknown) =>
  new Response(JSON.stringify(body), {
    status,
    headers: { 'Content-Type': 'application/json' },
  })

afterEach(() => vi.unstubAllGlobals())

it('searchCatalog encodes the query', async () => {
  const fetchMock = vi.fn().mockResolvedValue(jsonResponse(200, { degraded: false, results: [] }))
  vi.stubGlobal('fetch', fetchMock)
  await searchCatalog('game', 'chrono & co')
  expect(fetchMock).toHaveBeenCalledWith('/api/search?type=game&q=chrono+%26+co')
})

it('resolveProduct posts the selection', async () => {
  const fetchMock = vi.fn().mockResolvedValue(jsonResponse(200, { id: 'p1', type: 'game', name: 'CT' }))
  vi.stubGlobal('fetch', fetchMock)
  const p = await resolveProduct({ type: 'game', igdb_game_id: 1000, platform_igdb_id: 6 })
  expect(p.id).toBe('p1')
  expect(fetchMock.mock.calls[0][0]).toBe('/api/products/resolve')
})

it('fetchRecommendations reads the composed endpoint', async () => {
  const fetchMock = vi.fn().mockResolvedValue(jsonResponse(200, { degraded: false, recommendations: [] }))
  vi.stubGlobal('fetch', fetchMock)
  await fetchRecommendations()
  expect(fetchMock).toHaveBeenCalledWith('/api/recommendations')
})
