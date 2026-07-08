import type { PlatformFacet, Tag } from '../../api/collection'
import type { GroupBy, ListState, Sort } from '../../lib/listParams'
import {
  canBacklogSort, CONDITIONS, defaultListState, GROUPS, ITEM_TYPES, PACKAGINGS, REGIONS, SORTS, STATUSES,
} from '../../lib/listParams'

const sortLabels: Record<Sort, string> = {
  name: 'Name',
  release_date: 'Release date',
  purchased_at: 'Purchase date',
  created_at: 'Date added',
  value: 'Value',
  paid: 'Price paid',
  rating: 'Rating',
  backlog_rank: 'Backlog order',
}

const groupLabels: Record<GroupBy, string> = {
  platform: 'Platform',
  status: 'Status',
  item_type: 'Item type',
  location: 'Location',
  tag: 'Tag',
}

const chipLabels: Record<string, string> = {
  backlog: 'Backlog', playing: 'Playing', beaten: 'Beaten', completed: 'Completed',
  dropped: 'Dropped', shelved: 'Shelved',
  game: 'Games', console: 'Consoles', accessory: 'Accessories',
  sealed: 'Sealed', cib: 'CIB', loose: 'Loose',
  ntsc_u: 'NTSC-U', ntsc_j: 'NTSC-J', pal: 'PAL', region_free: 'Region free',
  mint: 'Mint', near_mint: 'Near mint', very_good: 'Very good', good: 'Good',
  acceptable: 'Acceptable', poor: 'Poor',
}

interface FilterBarProps {
  state: ListState
  platforms: PlatformFacet[]
  tags: Tag[]
  onChange: (next: ListState) => void
}

export default function FilterBar({ state, platforms, tags, onChange }: FilterBarProps) {
  function toggled<T>(list: T[], v: T): T[] {
    return list.includes(v) ? list.filter((x) => x !== v) : [...list, v]
  }

  const chipGroup = <T extends string>(legend: string, all: readonly T[], key: keyof ListState) => (
    <fieldset className="flex flex-wrap items-center gap-2">
      <legend className="float-left mr-2 text-xs font-semibold uppercase tracking-wide text-gray-500">
        {legend}
      </legend>
      {all.map((v) => (
        <label key={v} className="flex items-center gap-1 text-sm">
          <input
            type="checkbox"
            checked={(state[key] as T[]).includes(v)}
            onChange={() => onChange({ ...state, [key]: toggled(state[key] as T[], v) })}
          />
          {chipLabels[v] ?? v}
        </label>
      ))}
    </fieldset>
  )

  const sorts = SORTS.filter((s) => s !== 'backlog_rank' || canBacklogSort(state))

  return (
    <section aria-label="Filters" className="mb-4 flex flex-col gap-2 rounded border border-gray-200 p-3">
      {chipGroup('Status', STATUSES, 'status')}
      {chipGroup('Type', ITEM_TYPES, 'itemType')}
      {chipGroup('Packaging', PACKAGINGS, 'packaging')}
      {chipGroup('Region', REGIONS, 'region')}
      {chipGroup('Condition', CONDITIONS, 'itemCondition')}
      <fieldset className="flex flex-wrap items-center gap-2">
        <legend className="float-left mr-2 text-xs font-semibold uppercase tracking-wide text-gray-500">
          Platform
        </legend>
        {platforms.map((p) => (
          <label key={p.id} className="flex items-center gap-1 text-sm">
            <input
              type="checkbox"
              checked={state.platformId.includes(p.id)}
              onChange={() => onChange({ ...state, platformId: toggled(state.platformId, p.id) })}
            />
            {p.name}
          </label>
        ))}
        {platforms.length === 0 && <span className="text-xs text-gray-400">No platforms yet</span>}
      </fieldset>
      <fieldset className="flex flex-wrap items-center gap-2">
        <legend className="float-left mr-2 text-xs font-semibold uppercase tracking-wide text-gray-500">
          Tags (all of)
        </legend>
        {tags.map((t) => (
          <label key={t.id} className="flex items-center gap-1 text-sm">
            <input
              type="checkbox"
              checked={state.tagId.includes(t.id)}
              onChange={() => onChange({ ...state, tagId: toggled(state.tagId, t.id) })}
            />
            {t.name}
          </label>
        ))}
        {tags.length === 0 && <span className="text-xs text-gray-400">No tags yet</span>}
      </fieldset>

      <div className="flex flex-wrap items-end gap-3 border-t border-gray-100 pt-2">
        <label className="flex flex-col gap-1 text-sm font-medium">
          Sort
          <select
            value={state.sort ?? ''}
            onChange={(e) =>
              onChange({ ...state, sort: e.target.value === '' ? undefined : (e.target.value as Sort) })
            }
            className="rounded border border-gray-300 px-2 py-1 text-sm"
          >
            <option value="">Date added (default)</option>
            {sorts.map((s) => (
              <option key={s} value={s}>
                {sortLabels[s]}
              </option>
            ))}
          </select>
        </label>
        <button
          type="button"
          onClick={() => onChange({ ...state, order: state.order === 'asc' ? 'desc' : 'asc' })}
          className="rounded border border-gray-300 px-2 py-1 text-sm hover:bg-gray-50"
        >
          Order: {(state.order ?? 'desc') === 'desc' ? 'descending' : 'ascending'}
        </button>
        <label className="flex flex-col gap-1 text-sm font-medium">
          Group by
          <select
            value={state.groupBy ?? ''}
            onChange={(e) =>
              onChange({ ...state, groupBy: e.target.value === '' ? undefined : (e.target.value as GroupBy) })
            }
            className="rounded border border-gray-300 px-2 py-1 text-sm"
          >
            <option value="">Ungrouped</option>
            {GROUPS.map((g) => (
              <option key={g} value={g}>
                {groupLabels[g]}
              </option>
            ))}
          </select>
        </label>
        <button
          type="button"
          onClick={() => onChange({ ...defaultListState(), mode: state.mode })}
          className="ml-auto rounded border border-gray-300 px-2 py-1 text-sm text-gray-600 hover:bg-gray-50"
        >
          Clear filters
        </button>
      </div>
    </section>
  )
}
