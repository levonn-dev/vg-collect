import { fetchPlatforms } from './platforms'

const jsonResponse = (status: number, body: unknown) =>
  new Response(JSON.stringify(body), { status, headers: { 'Content-Type': 'application/json' } })

afterEach(() => vi.unstubAllGlobals())

it('fetchPlatforms reads the catalog', async () => {
  const fetchMock = vi.fn().mockResolvedValue(jsonResponse(200, { platforms: [] }))
  vi.stubGlobal('fetch', fetchMock)
  await fetchPlatforms()
  expect(fetchMock).toHaveBeenCalledWith('/api/platforms')
})
