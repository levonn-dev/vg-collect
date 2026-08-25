// Sums loaded rows into the next offset, undefined once caught up with
// total_count; countPage varies by response shape (flat vs grouped).
export function offsetNextPageParam<Page extends { total_count: number }>(
  last: Page,
  pages: Page[],
  countPage: (page: Page) => number,
): number | undefined {
  const loaded = pages.reduce((n, page) => n + countPage(page), 0)
  return loaded < last.total_count ? loaded : undefined
}
