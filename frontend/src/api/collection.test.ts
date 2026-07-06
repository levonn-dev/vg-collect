import {
  createTag, deleteEntry, fetchEntries, fetchPlatformFacets, fetchTags, fetchViews, reorderEntry,
} from './collection'

const jsonResponse = (status: number, body: unknown) =>
  new Response(JSON.stringify(body), {
    status,
    headers: { 'Content-Type': 'application/json' },
  })

afterEach(() => vi.unstubAllGlobals())

it('fetchEntries appends the query string only when present', async () => {
  const emptyList = { pricing_available: true, total_count: 0, entries: [] }
  const fetchMock = vi.fn()
    .mockResolvedValueOnce(jsonResponse(200, emptyList))
    .mockResolvedValueOnce(jsonResponse(200, emptyList))
  vi.stubGlobal('fetch', fetchMock)
  await fetchEntries(new URLSearchParams())
  expect(fetchMock).toHaveBeenLastCalledWith('/api/entries')
  await fetchEntries(new URLSearchParams({ sort: 'name' }))
  expect(fetchMock).toHaveBeenLastCalledWith('/api/entries?sort=name')
})

it('fetchTags and fetchViews unwrap their envelopes', async () => {
  vi.stubGlobal('fetch', vi.fn()
    .mockResolvedValueOnce(jsonResponse(200, { tags: [{ id: 't1', name: 'rpg', entry_count: 2 }] }))
    .mockResolvedValueOnce(jsonResponse(200, { views: [] })))
  expect(await fetchTags()).toEqual([{ id: 't1', name: 'rpg', entry_count: 2 }])
  expect(await fetchViews()).toEqual([])
})

it('createTag posts the name and reorderEntry posts the neighbor pair', async () => {
  const fetchMock = vi.fn()
    .mockResolvedValueOnce(jsonResponse(201, { id: 't1', name: 'rpg', entry_count: 0 }))
    .mockResolvedValueOnce(jsonResponse(200, { id: 'e1' }))
  vi.stubGlobal('fetch', fetchMock)
  await createTag('rpg')
  expect(fetchMock.mock.calls[0][0]).toBe('/api/tags')
  await reorderEntry('e1', { after_id: null, before_id: 'e2' })
  expect(fetchMock.mock.calls[1][0]).toBe('/api/entries/e1/reorder')
  expect((fetchMock.mock.calls[1][1] as RequestInit).body).toBe(JSON.stringify({ after_id: null, before_id: 'e2' }))
})

it('deleteEntry tolerates 204', async () => {
  vi.stubGlobal('fetch', vi.fn().mockResolvedValue(new Response(null, { status: 204 })))
  await expect(deleteEntry('e1')).resolves.toBeUndefined()
})

const facetEntry = (id: number, name: string) => ({ platform: { igdb_platform_id: id, name } })

it('fetchPlatformFacets pages until total_count and dedupes sorted by name', async () => {
  const pageOne = Array.from({ length: 500 }, () => facetEntry(6, 'SNES'))
  const fetchMock = vi.fn()
    .mockResolvedValueOnce(jsonResponse(200, { pricing_available: true, total_count: 501, entries: pageOne }))
    .mockResolvedValueOnce(jsonResponse(200, {
      pricing_available: true, total_count: 501,
      entries: [facetEntry(7, 'PlayStation'), { display_name: 'custom, no platform' }],
    }))
  vi.stubGlobal('fetch', fetchMock)
  const facets = await fetchPlatformFacets()
  expect(fetchMock).toHaveBeenCalledTimes(2)
  expect(fetchMock.mock.calls[1][0]).toBe('/api/entries?limit=500&offset=500')
  expect(facets).toEqual([{ id: 7, name: 'PlayStation' }, { id: 6, name: 'SNES' }])
})
