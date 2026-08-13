import { fetchPlatforms } from './platforms'
import { jsonResponse } from '../test/fixtures'

afterEach(() => vi.unstubAllGlobals())

it('fetchPlatforms reads the catalog', async () => {
  const fetchMock = vi.fn().mockResolvedValue(jsonResponse(200, { platforms: [] }))
  vi.stubGlobal('fetch', fetchMock)
  await fetchPlatforms()
  expect(fetchMock).toHaveBeenCalledWith('/api/platforms')
})
