import type { components } from './schema'
import { getJSON, sendJSON } from './client'

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

export function fetchEntries(query: URLSearchParams): Promise<EntryList> {
  const qs = query.toString()
  return getJSON<EntryList>(qs ? `/api/entries?${qs}` : '/api/entries')
}

export function fetchEntry(id: string): Promise<Entry> {
  return getJSON<Entry>(`/api/entries/${id}`)
}

export function createEntry(body: EntryCreate): Promise<Entry> {
  return sendJSON<Entry>('POST', '/api/entries', body)
}

export function updateEntry(id: string, body: EntryUpdate): Promise<Entry> {
  return sendJSON<Entry>('PUT', `/api/entries/${id}`, body)
}

export function deleteEntry(id: string): Promise<void> {
  return sendJSON<void>('DELETE', `/api/entries/${id}`)
}

// Dismisses the region-mismatch banner for the entry's current
// (region, product) choice; the collection service clears the stamp
// again whenever either changes, so the banner notifies once more.
export function ackRegionMismatch(id: string): Promise<void> {
  return sendJSON<void>('POST', `/api/entries/${id}/region-mismatch-ack`)
}

export function reorderEntry(id: string, body: ReorderRequest): Promise<Entry> {
  return sendJSON<Entry>('POST', `/api/entries/${id}/reorder`, body)
}

// bulkUpdateEntries applies a tag/status/storage-location delta across
// a batch of the caller's own entries in one transaction. Absent
// fields stay untouched; storage_location alone clears on an explicit
// empty string (the opposite of updateEntry's full-replacement rule).
export function bulkUpdateEntries(body: BulkUpdateRequest): Promise<BulkUpdateResult> {
  return sendJSON<BulkUpdateResult>('POST', '/api/entries/bulk-update', body)
}

export async function fetchTags(): Promise<Tag[]> {
  const body = await getJSON<{ tags: Tag[] }>('/api/tags')
  return body.tags
}

export function createTag(name: string): Promise<Tag> {
  return sendJSON<Tag>('POST', '/api/tags', { name })
}

export async function fetchViews(): Promise<SavedView[]> {
  const body = await getJSON<{ views: SavedView[] }>('/api/views')
  return body.views
}

export function createView(name: string, params: Record<string, unknown>): Promise<SavedView> {
  return sendJSON<SavedView>('POST', '/api/views', { name, params })
}

export function updateView(
  id: string,
  name: string,
  params: Record<string, unknown>,
  visibility: SavedView['visibility'],
): Promise<SavedView> {
  return sendJSON<SavedView>('PUT', `/api/views/${id}`, { name, params, visibility })
}

export function deleteView(id: string): Promise<void> {
  return sendJSON<void>('DELETE', `/api/views/${id}`)
}

export function fetchDashboard(query?: URLSearchParams): Promise<Dashboard> {
  const qs = query?.toString() ?? ''
  return getJSON<Dashboard>(qs ? `/api/dashboard?${qs}` : '/api/dashboard')
}

export function fetchValueHistory(): Promise<ValueHistory> {
  return getJSON<ValueHistory>('/api/dashboard/value-history')
}

export interface PlatformFacet {
  id: number
  name: string
}

export interface EntryFacets {
  platforms: PlatformFacet[]
  developers: string[]
  publishers: string[]
}

// fetchEntryFacets derives the filter choices from the collection
// itself (there is no facet endpoint): it pages the flat list at the
// server's maximum page size and collects distinct platform snapshots
// and credit names in one sweep. Collections are person-scale, so
// this is a few requests at worst; the page cap is a safety stop, not
// a real limit.
export async function fetchEntryFacets(): Promise<EntryFacets> {
  const seen = new Map<number, string>()
  const developers = new Set<string>()
  const publishers = new Set<string>()
  const limit = 500
  for (let page = 0; page < 10; page++) {
    const q = new URLSearchParams({ limit: String(limit), offset: String(page * limit) })
    const res = await fetchEntries(q)
    for (const e of res.entries ?? []) {
      if (e.platform?.igdb_platform_id !== undefined) {
        seen.set(e.platform.igdb_platform_id, e.platform.name)
      }
      for (const d of e.developers ?? []) developers.add(d)
      for (const p of e.publishers ?? []) publishers.add(p)
    }
    if ((page + 1) * limit >= res.total_count) break
  }
  return {
    platforms: [...seen.entries()]
      .map(([id, name]) => ({ id, name }))
      .sort((a, b) => a.name.localeCompare(b.name)),
    developers: [...developers].sort((a, b) => a.localeCompare(b)),
    publishers: [...publishers].sort((a, b) => a.localeCompare(b)),
  }
}
