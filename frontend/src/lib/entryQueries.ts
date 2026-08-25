import type { QueryClient, QueryKey } from '@tanstack/react-query'

// Invalidates entries, dashboard, and recommendation weights; extras
// appends call-site-only keys (facets, tags). Outside-family keys
// (e.g. entry/product) are invalidated by the caller around this call.
export function invalidateEntryQueries(qc: QueryClient, extras: QueryKey[] = []): void {
  void qc.invalidateQueries({ queryKey: ['entries'] })
  void qc.invalidateQueries({ queryKey: ['dashboard'] })
  void qc.invalidateQueries({ queryKey: ['recommendations'] })
  for (const key of extras) void qc.invalidateQueries({ queryKey: key })
}
