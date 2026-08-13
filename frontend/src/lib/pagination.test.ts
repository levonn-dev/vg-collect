import { offsetNextPageParam } from './pagination'

interface Page {
  total_count: number
  items: string[]
}

const page = (items: string[], total_count: number): Page => ({ items, total_count })
const countItems = (p: Page) => p.items.length

it('returns the running offset while more rows remain', () => {
  const pages = [page(['a', 'b'], 5)]
  expect(offsetNextPageParam(pages[0], pages, countItems)).toBe(2)
})

it('sums every loaded page, not just the last one', () => {
  const pages = [page(['a', 'b'], 5), page(['c', 'd'], 5)]
  expect(offsetNextPageParam(pages[pages.length - 1], pages, countItems)).toBe(4)
})

it('returns undefined once loaded catches up with total_count', () => {
  const pages = [page(['a', 'b'], 2)]
  expect(offsetNextPageParam(pages[0], pages, countItems)).toBeUndefined()
})

it('returns undefined for an empty total (nothing to page)', () => {
  const pages = [page([], 0)]
  expect(offsetNextPageParam(pages[0], pages, countItems)).toBeUndefined()
})

it('lets the caller count a page by a different shape than a flat array', () => {
  interface GroupedPage {
    total_count: number
    entries?: string[]
    groups?: { entries: string[] }[]
  }
  const flat: GroupedPage = { total_count: 3, entries: ['a', 'b'] }
  const grouped: GroupedPage = { total_count: 3, groups: [{ entries: ['x'] }] }
  const count = (p: GroupedPage) =>
    p.entries?.length ?? p.groups?.reduce((m, g) => m + g.entries.length, 0) ?? 0
  expect(offsetNextPageParam(flat, [flat], count)).toBe(2)
  expect(offsetNextPageParam(grouped, [grouped], count)).toBe(1)
})
