import type { components } from './schema'
import { api, unwrap } from './client'

export type Entry = components['schemas']['Entry']
export type EntryCreate = components['schemas']['EntryCreate']
export type EntryUpdate = components['schemas']['EntryUpdate']
export type EntryList = components['schemas']['EntryList']
export type EntryGroup = components['schemas']['EntryGroup']
export type ReorderRequest = components['schemas']['ReorderRequest']
export type BulkUpdateRequest = components['schemas']['BulkUpdateRequest']
export type BulkUpdateResult = components['schemas']['BulkUpdateResult']
export type Tag = components['schemas']['Tag']
export type TagRef = components['schemas']['TagRef']
export type SavedView = components['schemas']['SavedView']
export type Dashboard = components['schemas']['Dashboard']
export type ValueHistory = components['schemas']['ValueHistory']

// The list endpoints take their query from the URL-first list state as
// a ready URLSearchParams, so the codec stays the single serializer:
// the params pass through verbatim (openapi-fetch appends no ? when
// the serializer returns an empty string).
export async function fetchEntries(query: URLSearchParams): Promise<EntryList> {
  return unwrap(await api.GET('/api/entries', { querySerializer: () => query.toString() }))
}

export async function fetchEntry(id: string): Promise<Entry> {
  return unwrap(await api.GET('/api/entries/{entryId}', { params: { path: { entryId: id } } }))
}

export async function createEntry(body: EntryCreate): Promise<Entry> {
  return unwrap(await api.POST('/api/entries', { body }))
}

export async function updateEntry(id: string, body: EntryUpdate): Promise<Entry> {
  return unwrap(await api.PUT('/api/entries/{entryId}', { params: { path: { entryId: id } }, body }))
}

export async function deleteEntry(id: string): Promise<void> {
  return unwrap<void>(await api.DELETE('/api/entries/{entryId}', { params: { path: { entryId: id } } }))
}

// Dismisses the region-mismatch banner for the entry's current
// (region, product) choice; the collection service clears the stamp
// again whenever either changes, so the banner notifies once more.
export async function ackRegionMismatch(id: string): Promise<void> {
  return unwrap<void>(
    await api.POST('/api/entries/{entryId}/region-mismatch-ack', { params: { path: { entryId: id } } }),
  )
}

export async function reorderEntry(id: string, body: ReorderRequest): Promise<Entry> {
  return unwrap(
    await api.POST('/api/entries/{entryId}/reorder', { params: { path: { entryId: id } }, body }),
  )
}

// bulkUpdateEntries applies a tag/status/storage-location delta across
// a batch of the caller's own entries in one transaction. Absent
// fields stay untouched; storage_location alone clears on an explicit
// empty string (the opposite of updateEntry's full-replacement rule).
export async function bulkUpdateEntries(body: BulkUpdateRequest): Promise<BulkUpdateResult> {
  return unwrap(await api.POST('/api/entries/bulk-update', { body }))
}

export async function fetchTags(): Promise<Tag[]> {
  const body = await unwrap(await api.GET('/api/tags'))
  return body.tags
}

export async function createTag(name: string): Promise<Tag> {
  return unwrap(await api.POST('/api/tags', { body: { name } }))
}

export async function fetchViews(): Promise<SavedView[]> {
  const body = await unwrap(await api.GET('/api/views'))
  return body.views
}

// The generated ViewCreate type marks visibility required (the
// generator treats a schema default as always-present), but the
// contract declares it optional with default private - the cast keeps
// the wire omitting it, so creation stays server-defaulted.
export async function createView(name: string, params: Record<string, unknown>): Promise<SavedView> {
  const body = { name, params } as components['schemas']['ViewCreate']
  return unwrap(await api.POST('/api/views', { body }))
}

export async function updateView(
  id: string,
  name: string,
  params: Record<string, unknown>,
  visibility: SavedView['visibility'],
): Promise<SavedView> {
  return unwrap(
    await api.PUT('/api/views/{viewId}', {
      params: { path: { viewId: id } },
      body: { name, params, visibility },
    }),
  )
}

export async function deleteView(id: string): Promise<void> {
  return unwrap<void>(await api.DELETE('/api/views/{viewId}', { params: { path: { viewId: id } } }))
}

export async function fetchDashboard(query?: URLSearchParams): Promise<Dashboard> {
  return unwrap(await api.GET('/api/dashboard', { querySerializer: () => query?.toString() ?? '' }))
}

export async function fetchValueHistory(): Promise<ValueHistory> {
  return unwrap(await api.GET('/api/dashboard/value-history'))
}
