import type { paths } from '../api/schema'

type ListQuery = NonNullable<paths['/api/entries']['get']['parameters']['query']>

export type ItemType = NonNullable<ListQuery['item_type']>[number]
export type Status = NonNullable<ListQuery['status']>[number]
export type Packaging = NonNullable<ListQuery['packaging']>[number]
// The known entry regions - the machinery key set (labels, pricing
// class, localization chains). region itself is open-world on the
// wire; these four are what the UI offers first-class and what the
// filter buckets cover.
export type Region = 'ntsc_u' | 'ntsc_j' | 'pal' | 'region_free'
export type Condition = NonNullable<ListQuery['item_condition']>[number]
export type Sort = NonNullable<ListQuery['sort']>
export type Order = NonNullable<ListQuery['order']>
export type GroupBy = NonNullable<ListQuery['group_by']>
export type ViewMode = 'table' | 'grid' | 'compact'

export const PAGE_SIZE = 200

export const ITEM_TYPES: ItemType[] = ['game', 'console', 'accessory']
export const STATUSES: Status[] = ['backlog', 'playing', 'beaten', 'completed', 'dropped', 'shelved']
export const PACKAGINGS: Packaging[] = ['sealed', 'cib', 'loose']
export const REGIONS: Region[] = ['ntsc_u', 'ntsc_j', 'pal', 'region_free']
export const CONDITIONS: Condition[] = ['mint', 'near_mint', 'very_good', 'good', 'acceptable', 'poor']
export const SORTS: Sort[] = ['name', 'release_date', 'purchased_at', 'created_at', 'value', 'paid', 'rating', 'backlog_rank']
export const GROUPS: GroupBy[] = ['platform', 'status', 'item_type', 'location', 'tag']

// ListState is the single source of collection-view state. The URL is
// its persistence (shareable, back-button friendly) and shelves
// serialize the same shape.
export interface ListState {
  itemType: ItemType[]
  status: Status[]
  packaging: Packaging[]
  region: Region[]
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
    itemType: [], status: [], packaging: [], region: [], itemCondition: [],
    platformId: [], tagId: [], page: 0, mode: 'table',
  }
}

// canBacklogSort: the drag board only makes sense over exactly the
// backlog (ranks exist only there).
export function canBacklogSort(s: ListState): boolean {
  return s.status.length === 1 && s.status[0] === 'backlog'
}

function appendFilters(q: URLSearchParams, s: ListState): void {
  for (const v of s.itemType) q.append('item_type', v)
  for (const v of s.status) q.append('status', v)
  for (const v of s.packaging) q.append('packaging', v)
  for (const v of s.region) q.append('region', v)
  for (const v of s.itemCondition) q.append('item_condition', v)
  for (const v of s.platformId) q.append('platform_id', String(v))
  for (const v of s.tagId) q.append('tag_id', v)
}

// toQuery builds the /api/entries request. RULE: a backlog_rank read
// always sends order=asc explicitly. The contract-wide order default
// is desc (right for newest-first created_at listings), but applied to
// rank it reverses drag order, and the board's visual-neighbor
// reorder mapping would then disagree with rank adjacency and the
// server would answer 409 conflicting_order.
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

// toFilterQuery serializes only the filter dimensions - the slice the
// dashboard aggregates accept. Sort, grouping, paging, and view mode
// change how the list reads, not which entries are counted.
export function toFilterQuery(s: ListState): URLSearchParams {
  const q = new URLSearchParams()
  appendFilters(q, s)
  return q
}

function pick<T extends string>(all: readonly T[], values: string[]): T[] {
  return values.filter((v): v is T => (all as readonly string[]).includes(v))
}

// toSearchParams/fromSearchParams persist ListState in the URL,
// omitting defaults so plain routes stay clean. Unknown or invalid
// values are dropped, never thrown: URLs and stored view params are
// user-editable input.
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
  // A rank sort outside a pure-backlog filter has no meaning; drop it
  // rather than render an inconsistent board.
  if (s.sort === 'backlog_rank' && !canBacklogSort(s)) s.sort = undefined
  return s
}

// toViewParams/fromViewParams serialize the same state (minus paging
// and the view's own id) into a saved view's opaque params JSON. The
// v marker lets a future shape change branch instead of guess.
export function toViewParams(s: ListState): Record<string, unknown> {
  return {
    v: 1,
    item_type: s.itemType,
    status: s.status,
    packaging: s.packaging,
    region: s.region,
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
  for (const key of ['item_type', 'status', 'packaging', 'region', 'item_condition', 'platform_id', 'tag_id']) {
    appendAll(key)
  }
  for (const key of ['sort', 'order', 'group_by', 'mode'] as const) {
    const v = p[key]
    if (typeof v === 'string') sp.set(key, v)
  }
  return fromSearchParams(sp)
}
