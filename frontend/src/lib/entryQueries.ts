import type { QueryClient, QueryKey } from '@tanstack/react-query'

// invalidateEntryQueries centralizes the invalidation triple every
// entry-mutating action needs: the list itself, the dashboard stats
// derived from it (InsightsPanel/StatCards), and recommendation
// weights (a status or pricing change can move both). extras covers
// what only some call sites also touch - entry-facets on a new
// platform/credit name, tags on a bulk tag edit - appended after the
// triple in the order given. A mutation that must invalidate
// something OUTSIDE this family (PricingPanel's own ['entry', id] and
// ['product', id]) still issues those calls itself, around this one.
export function invalidateEntryQueries(qc: QueryClient, extras: QueryKey[] = []): void {
  void qc.invalidateQueries({ queryKey: ['entries'] })
  void qc.invalidateQueries({ queryKey: ['dashboard'] })
  void qc.invalidateQueries({ queryKey: ['recommendations'] })
  for (const key of extras) void qc.invalidateQueries({ queryKey: key })
}
