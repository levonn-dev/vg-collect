import { fetchEntries } from '../api/collection'
import { operationParams } from '../gen/facets'

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
  const limit = operationParams.listEntries.limit.maximum
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
