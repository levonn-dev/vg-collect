import {
  bulkUpdateEntries, createTag, deleteEntry, fetchEntries, fetchTags, fetchViews, reorderEntry,
} from './collection'
import { calledPath, jsonResponse } from '../test/fixtures'

afterEach(() => vi.unstubAllGlobals())

it('fetchEntries appends the query string only when present', async () => {
  const emptyList = { pricing_available: true, total_count: 0, entries: [] }
  const fetchMock = vi.fn()
    .mockResolvedValueOnce(jsonResponse(200, emptyList))
    .mockResolvedValueOnce(jsonResponse(200, emptyList))
  vi.stubGlobal('fetch', fetchMock)
  await fetchEntries(new URLSearchParams())
  expect(calledPath(fetchMock)).toBe('/api/entries')
  await fetchEntries(new URLSearchParams({ sort: 'name' }))
  expect(calledPath(fetchMock)).toBe('/api/entries?sort=name')
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
  expect(calledPath(fetchMock, 0)).toBe('/api/tags')
  await reorderEntry('e1', { after_id: null, before_id: 'e2' })
  expect(calledPath(fetchMock, 1)).toBe('/api/entries/e1/reorder')
  const req = fetchMock.mock.calls[1][0] as Request
  expect(await req.text()).toBe(JSON.stringify({ after_id: null, before_id: 'e2' }))
})

it('bulkUpdateEntries posts to the batch endpoint and unwraps the count', async () => {
  vi.stubGlobal('fetch', vi.fn().mockResolvedValue(jsonResponse(200, { updated_count: 2 })))
  const result = await bulkUpdateEntries({ entry_ids: ['e1', 'e2'], status: 'shelved' })
  expect(result).toEqual({ updated_count: 2 })
  const fetchMock = vi.mocked(fetch)
  expect(calledPath(fetchMock, 0)).toBe('/api/entries/bulk-update')
  const req = fetchMock.mock.calls[0][0] as Request
  expect(req.method).toBe('POST')
  expect(await req.text()).toBe(JSON.stringify({ entry_ids: ['e1', 'e2'], status: 'shelved' }))
})

it('deleteEntry tolerates 204', async () => {
  vi.stubGlobal('fetch', vi.fn().mockResolvedValue(new Response(null, { status: 204 })))
  await expect(deleteEntry('e1')).resolves.toBeUndefined()
})
