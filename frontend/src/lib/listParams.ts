import type { paths } from '../api/schema'
import { entryStatusValues, itemConditionValues, itemTypeValues, packagingValues } from '../api/schema'
import { REGIONS } from '../gen/domain'

type ListQuery = NonNullable<paths['/api/entries']['get']['parameters']['query']>

// Derived from the generated schema's enum arrays (single source of
// truth); kept under existing names so importers compile unchanged.
export const ITEM_TYPES = itemTypeValues
export const STATUSES = entryStatusValues
export const PACKAGINGS = packagingValues
export const CONDITIONS = itemConditionValues
export type ItemType = (typeof ITEM_TYPES)[number]
export type Status = (typeof STATUSES)[number]
export type Packaging = (typeof PACKAGINGS)[number]
export type Condition = (typeof CONDITIONS)[number]
// Known entry regions the UI offers first-class (wire region itself is
// open-world). Generated from api/domain.yaml; REGIONS re-exported below.
export type Region = (typeof REGIONS)[number]
export type Sort = NonNullable<ListQuery['sort']>
export type Order = NonNullable<ListQuery['order']>
export type GroupBy = NonNullable<ListQuery['group_by']>
export type ViewMode = 'table' | 'grid' | 'compact'

// Product paging choice, well under the contract's entries-list limit (500).
export const PAGE_SIZE = 200

// Highest valid zero-based page for a total; shared by Pager's bound
// and Collection's stale-page clamp. Needs a real total_count, so
// URL-only callers (fromSearchParams) can't make this call.
export function lastPage(totalCount: number): number {
  return Math.max(0, Math.ceil(totalCount / PAGE_SIZE) - 1)
}

export { REGIONS }
export const SORTS: Sort[] = ['name', 'release_date', 'purchased_at', 'created_at', 'value', 'paid', 'rating', 'backlog_rank']
export const GROUPS: GroupBy[] = ['platform', 'status', 'item_type', 'location', 'tag']

// Single source of collection-view state; URL is its persistence,
// shelves serialize the same shape.
export interface ListState {
  itemType: ItemType[]
  status: Status[]
  packaging: Packaging[]
  region: Region[]
  // Open-world snapshot facts (IGDB and community names alike),
  // matched by array overlap server-side.
  developer: string[]
  publisher: string[]
  itemCondition: Condition[]
  platformId: number[]
  tagId: string[]
  sort?: Sort
  order?: Order
  groupBy?: GroupBy
  page: number
  mode: ViewMode
  viewId?: string
}

export function defaultListState(): ListState {
  return {
    itemType: [], status: [], packaging: [], region: [], developer: [], publisher: [], itemCondition: [],
    platformId: [], tagId: [], page: 0, mode: 'table',
  }
}

// Drag board only makes sense over exactly the backlog (ranks exist only there).
export function canBacklogSort(s: ListState): boolean {
  return s.status.length === 1 && s.status[0] === 'backlog'
}

function appendFilters(q: URLSearchParams, s: ListState): void {
  for (const v of s.itemType) q.append('item_type', v)
  for (const v of s.status) q.append('status', v)
  for (const v of s.packaging) q.append('packaging', v)
  for (const v of s.region) q.append('region', v)
  for (const v of s.developer) q.append('developer', v)
  for (const v of s.publisher) q.append('publisher', v)
  for (const v of s.itemCondition) q.append('item_condition', v)
  for (const v of s.platformId) q.append('platform_id', String(v))
  for (const v of s.tagId) q.append('tag_id', v)
}

// backlog_rank always sends order=asc explicitly: the contract-wide
// desc default would reverse drag order and the server answers 409
// conflicting_order.
export function toQuery(s: ListState): URLSearchParams {
  const q = new URLSearchParams()
  appendFilters(q, s)
  if (s.sort === 'backlog_rank') {
    q.set('sort', 'backlog_rank')
    q.set('order', 'asc')
  } else {
    if (s.sort) q.set('sort', s.sort)
    if (s.order) q.set('order', s.order)
  }
  if (s.groupBy) q.set('group_by', s.groupBy)
  q.set('limit', String(PAGE_SIZE))
  if (s.page > 0) q.set('offset', String(s.page * PAGE_SIZE))
  return q
}

// Filter dimensions only, the slice dashboard aggregates accept;
// sort/paging/mode don't change which entries count.
export function toFilterQuery(s: ListState): URLSearchParams {
  const q = new URLSearchParams()
  appendFilters(q, s)
  return q
}

function pick<T extends string>(all: readonly T[], values: string[]): T[] {
  return values.filter((v): v is T => (all as readonly string[]).includes(v))
}

// Persist ListState in the URL, omitting defaults. Unknown/invalid
// values drop, never throw: URLs are user-editable.
export function toSearchParams(s: ListState): URLSearchParams {
  const sp = new URLSearchParams()
  appendFilters(sp, s)
  if (s.sort) sp.set('sort', s.sort)
  if (s.order) sp.set('order', s.order)
  if (s.groupBy) sp.set('group_by', s.groupBy)
  if (s.page > 0) sp.set('page', String(s.page))
  if (s.mode !== 'table') sp.set('mode', s.mode)
  if (s.viewId) sp.set('shelf', s.viewId)
  return sp
}

export function fromSearchParams(sp: URLSearchParams): ListState {
  const s = defaultListState()
  s.itemType = pick(ITEM_TYPES, sp.getAll('item_type'))
  s.status = pick(STATUSES, sp.getAll('status'))
  s.packaging = pick(PACKAGINGS, sp.getAll('packaging'))
  s.region = pick(REGIONS, sp.getAll('region'))
  // Credits have no known set to pick against: verbatim pass-through.
  s.developer = sp.getAll('developer')
  s.publisher = sp.getAll('publisher')
  s.itemCondition = pick(CONDITIONS, sp.getAll('item_condition'))
  s.platformId = sp.getAll('platform_id').map(Number).filter((n) => Number.isInteger(n) && n > 0)
  s.tagId = sp.getAll('tag_id')
  const sort = sp.get('sort')
  if (sort !== null && (SORTS as string[]).includes(sort)) s.sort = sort as Sort
  const order = sp.get('order')
  if (order === 'asc' || order === 'desc') s.order = order
  const group = sp.get('group_by')
  if (group !== null && (GROUPS as string[]).includes(group)) s.groupBy = group as GroupBy
  const page = Number(sp.get('page') ?? '0')
  if (Number.isInteger(page) && page > 0) s.page = page
  const mode = sp.get('mode')
  if (mode === 'grid' || mode === 'compact') s.mode = mode
  const view = sp.get('shelf')
  if (view) s.viewId = view
  // Rank sort outside a pure-backlog filter is meaningless; drop it
  // rather than render an inconsistent board.
  if (s.sort === 'backlog_rank' && !canBacklogSort(s)) s.sort = undefined
  return s
}

// Serializes ListState (minus paging/viewId) into a saved view's
// opaque params JSON; v marker lets a future shape change branch, not guess.
export function toViewParams(s: ListState): Record<string, unknown> {
  return {
    v: 1,
    item_type: s.itemType,
    status: s.status,
    packaging: s.packaging,
    region: s.region,
    developer: s.developer,
    publisher: s.publisher,
    item_condition: s.itemCondition,
    platform_id: s.platformId,
    tag_id: s.tagId,
    sort: s.sort ?? null,
    order: s.order ?? null,
    group_by: s.groupBy ?? null,
    mode: s.mode,
  }
}

export function fromViewParams(p: Record<string, unknown>): ListState {
  const sp = new URLSearchParams()
  const appendAll = (key: string) => {
    const arr = p[key]
    if (!Array.isArray(arr)) return
    for (const v of arr) {
      if (typeof v === 'string' || typeof v === 'number') sp.append(key, String(v))
    }
  }
  for (const key of ['item_type', 'status', 'packaging', 'region', 'developer', 'publisher', 'item_condition', 'platform_id', 'tag_id']) {
    appendAll(key)
  }
  for (const key of ['sort', 'order', 'group_by', 'mode'] as const) {
    const v = p[key]
    if (typeof v === 'string') sp.set(key, v)
  }
  return fromSearchParams(sp)
}
