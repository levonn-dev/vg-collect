import {
  canBacklogSort, defaultListState, fromSearchParams, fromViewParams,
  PAGE_SIZE, toQuery, toSearchParams, toViewParams,
} from './listParams'

it('toQuery always pins order=asc for backlog_rank reads', () => {
  const s = defaultListState()
  s.status = ['backlog']
  s.sort = 'backlog_rank'
  s.order = 'desc' // even a stray desc must not survive
  const q = toQuery(s)
  expect(q.get('sort')).toBe('backlog_rank')
  expect(q.get('order')).toBe('asc')
})

it('toQuery sends repeats for multi-value filters, limit always, offset past page 0', () => {
  const s = defaultListState()
  s.status = ['backlog', 'playing']
  s.platformId = [6, 7]
  const first = toQuery(s)
  expect(first.getAll('status')).toEqual(['backlog', 'playing'])
  expect(first.getAll('platform_id')).toEqual(['6', '7'])
  expect(first.get('limit')).toBe(String(PAGE_SIZE))
  expect(first.get('offset')).toBeNull()
  s.page = 2
  expect(toQuery(s).get('offset')).toBe(String(2 * PAGE_SIZE))
})

it('toQuery omits sort and order when unset (server defaults apply)', () => {
  const q = toQuery(defaultListState())
  expect(q.get('sort')).toBeNull()
  expect(q.get('order')).toBeNull()
})

it('URL round-trip preserves state and omits defaults', () => {
  const s = defaultListState()
  s.status = ['beaten']
  s.tagId = ['t1', 't2']
  s.sort = 'value'
  s.order = 'desc'
  s.groupBy = 'platform'
  s.page = 1
  s.mode = 'grid'
  s.viewId = 'v9'
  expect(fromSearchParams(toSearchParams(s))).toEqual(s)
  expect(toSearchParams(defaultListState()).toString()).toBe('')
})

it('fromSearchParams drops garbage instead of throwing', () => {
  const sp = new URLSearchParams(
    'status=exploded&sort=nonsense&order=sideways&platform_id=abc&page=-3&mode=hologram',
  )
  expect(fromSearchParams(sp)).toEqual(defaultListState())
})

it('fromSearchParams drops a rank sort that lost its backlog filter', () => {
  const sp = new URLSearchParams('sort=backlog_rank&status=backlog&status=playing')
  expect(fromSearchParams(sp).sort).toBeUndefined()
})

it('canBacklogSort demands exactly the backlog status filter', () => {
  const s = defaultListState()
  expect(canBacklogSort(s)).toBe(false)
  s.status = ['backlog']
  expect(canBacklogSort(s)).toBe(true)
  s.status = ['backlog', 'playing']
  expect(canBacklogSort(s)).toBe(false)
})

it('shelf params round-trip without page or view id', () => {
  const s = defaultListState()
  s.itemType = ['game']
  s.itemCondition = ['mint']
  s.sort = 'rating'
  s.order = 'desc'
  s.mode = 'compact'
  s.page = 4
  s.viewId = 'v1'
  const restored = fromViewParams(toViewParams(s))
  expect(restored.page).toBe(0)
  expect(restored.viewId).toBeUndefined()
  expect(restored).toEqual({ ...s, page: 0, viewId: undefined })
})

it('fromViewParams survives hostile params objects', () => {
  expect(fromViewParams({})).toEqual(defaultListState())
  expect(fromViewParams({ status: 'not-an-array', sort: 7, mode: ['grid'] })).toEqual(defaultListState())
})

// Credit filters are open-world snapshot facts: both codecs carry
// them verbatim, with no known set to gate against (region's posture,
// minus even the known-value pick).
it('credit filters round-trip the URL and view params verbatim', () => {
  const s = defaultListState()
  s.developer = ['Retro Studios', 'Square']
  s.publisher = ['Nintendo']
  expect(fromSearchParams(toSearchParams(s))).toEqual(s)
  expect(fromViewParams(toViewParams(s))).toEqual(s)
})
