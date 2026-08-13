// offsetNextPageParam is the shared getNextPageParam reducer for every
// list paged by a running offset against a total_count carried on the
// last page: sum what has loaded so far and hand back that running
// offset as the next pageParam, or undefined once loaded has caught
// up with total_count. countPage stays a caller-supplied callback
// because "how many rows did this page contribute" differs by
// response shape (a flat array on most lists, entries-or-groups on a
// shared shelf's grouped view).
export function offsetNextPageParam<Page extends { total_count: number }>(
  last: Page,
  pages: Page[],
  countPage: (page: Page) => number,
): number | undefined {
  const loaded = pages.reduce((n, page) => n + countPage(page), 0)
  return loaded < last.total_count ? loaded : undefined
}
