import type { components } from './schema'
import { getJSON, sendJSON } from './client'

export type Entry = components['schemas']['Entry']
export type EntryCreate = components['schemas']['EntryCreate']
export type EntryUpdate = components['schemas']['EntryUpdate']
export type EntryList = components['schemas']['EntryList']
export type EntryGroup = components['schemas']['EntryGroup']
export type ReorderRequest = components['schemas']['ReorderRequest']
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

export function reorderEntry(id: string, body: ReorderRequest): Promise<Entry> {
  return sendJSON<Entry>('POST', `/api/entries/${id}/reorder`, body)
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

export function updateView(id: string, name: string, params: Record<string, unknown>): Promise<SavedView> {
  return sendJSON<SavedView>('PUT', `/api/views/${id}`, { name, params })
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

// fetchPlatformFacets derives the platform filter's choices from the
// collection itself (there is no facet endpoint): it pages the flat
// list at the server's maximum page size and collects distinct
// platform snapshots. Collections are person-scale, so this is a few
// requests at worst; the page cap is a safety stop, not a real limit.
export async function fetchPlatformFacets(): Promise<PlatformFacet[]> {
  const seen = new Map<number, string>()
  const limit = 500
  for (let page = 0; page < 10; page++) {
    const q = new URLSearchParams({ limit: String(limit), offset: String(page * limit) })
    const res = await fetchEntries(q)
    for (const e of res.entries ?? []) {
      if (e.platform?.igdb_platform_id !== undefined) {
        seen.set(e.platform.igdb_platform_id, e.platform.name)
      }
    }
    if ((page + 1) * limit >= res.total_count) break
  }
  return [...seen.entries()]
    .map(([id, name]) => ({ id, name }))
    .sort((a, b) => a.name.localeCompare(b.name))
}
