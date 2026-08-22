import { fetchPlatforms } from './platforms'
import { calledPath, jsonResponse } from '../test/fixtures'

afterEach(() => vi.unstubAllGlobals())

it('fetchPlatforms reads the catalog', async () => {
  const fetchMock = vi.fn().mockResolvedValue(jsonResponse(200, { platforms: [] }))
  vi.stubGlobal('fetch', fetchMock)
  await fetchPlatforms()
  expect(calledPath(fetchMock)).toBe('/api/platforms')
})
