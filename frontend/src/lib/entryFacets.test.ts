import { fetchEntryFacets } from './entryFacets'
import { calledPath, jsonResponse } from '../test/fixtures'

afterEach(() => vi.unstubAllGlobals())

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
  expect(calledPath(fetchMock, 1)).toBe('/api/entries?limit=500&offset=500')
  expect(facets.platforms).toEqual([{ id: 7, name: 'PlayStation' }, { id: 6, name: 'SNES' }])
  expect(facets.developers).toEqual(['Retro Studios', 'Square'])
  expect(facets.publishers).toEqual(['Nintendo'])
})
