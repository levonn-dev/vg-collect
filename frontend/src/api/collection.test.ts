import {
  bulkUpdateEntries, createTag, deleteEntry, fetchEntries, fetchEntryFacets, fetchTags, fetchViews, reorderEntry,
} from './collection'
import { jsonResponse } from '../test/fixtures'

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

it('bulkUpdateEntries posts to the batch endpoint and unwraps the count', async () => {
  vi.stubGlobal('fetch', vi.fn().mockResolvedValue(jsonResponse(200, { updated_count: 2 })))
  const result = await bulkUpdateEntries({ entry_ids: ['e1', 'e2'], status: 'shelved' })
  expect(result).toEqual({ updated_count: 2 })
  const fetchMock = vi.mocked(fetch)
  expect(fetchMock.mock.calls[0][0]).toBe('/api/entries/bulk-update')
  const init = fetchMock.mock.calls[0][1] as RequestInit
  expect(init.method).toBe('POST')
  expect(init.body).toBe(JSON.stringify({ entry_ids: ['e1', 'e2'], status: 'shelved' }))
})

it('deleteEntry tolerates 204', async () => {
  vi.stubGlobal('fetch', vi.fn().mockResolvedValue(new Response(null, { status: 204 })))
  await expect(deleteEntry('e1')).resolves.toBeUndefined()
})

const facetEntry = (id: number, name: string) => ({ platform: { igdb_platform_id: id, name } })

it('fetchEntryFacets pages until total_count, deduping platforms and credits sorted by name', async () => {
  const pageOne = Array.from({ length: 500 }, () => ({ ...facetEntry(6, 'SNES'), developers: ['Square'] }))
  const fetchMock = vi.fn()
    .mockResolvedValueOnce(jsonResponse(200, { pricing_available: true, total_count: 501, entries: pageOne }))
    .mockResolvedValueOnce(jsonResponse(200, {
      pricing_available: true, total_count: 501,
      entries: [
        { ...facetEntry(7, 'PlayStation'), developers: ['Retro Studios', 'Square'], publishers: ['Nintendo'] },
        { display_name: 'custom, no platform' },
      ],
    }))
  vi.stubGlobal('fetch', fetchMock)
  const facets = await fetchEntryFacets()
  expect(fetchMock).toHaveBeenCalledTimes(2)
  expect(fetchMock.mock.calls[1][0]).toBe('/api/entries?limit=500&offset=500')
  expect(facets.platforms).toEqual([{ id: 7, name: 'PlayStation' }, { id: 6, name: 'SNES' }])
  expect(facets.developers).toEqual(['Retro Studios', 'Square'])
  expect(facets.publishers).toEqual(['Nintendo'])
})
