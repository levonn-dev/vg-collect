import { fetchRecommendations, resolveProduct, searchCatalog } from './catalog'
import { calledPath, jsonResponse } from '../test/fixtures'

afterEach(() => vi.unstubAllGlobals())

it('searchCatalog encodes the query', async () => {
  const fetchMock = vi.fn().mockResolvedValue(jsonResponse(200, { degraded: false, results: [] }))
  vi.stubGlobal('fetch', fetchMock)
  await searchCatalog('game', 'chrono & co')
  expect(calledPath(fetchMock)).toBe('/api/search?type=game&q=chrono+%26+co')
})

it('resolveProduct posts the selection', async () => {
  const fetchMock = vi.fn().mockResolvedValue(jsonResponse(200, { id: 'p1', type: 'game', name: 'CT' }))
  vi.stubGlobal('fetch', fetchMock)
  const p = await resolveProduct({ type: 'game', igdb_game_id: 1000, platform_igdb_id: 6 })
  expect(p.id).toBe('p1')
  expect(calledPath(fetchMock, 0)).toBe('/api/products/resolve')
})

it('fetchRecommendations reads the composed endpoint', async () => {
  const fetchMock = vi.fn().mockResolvedValue(jsonResponse(200, { degraded: false, recommendations: [] }))
  vi.stubGlobal('fetch', fetchMock)
  await fetchRecommendations()
  expect(calledPath(fetchMock)).toBe('/api/recommendations')
})
