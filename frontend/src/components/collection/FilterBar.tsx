import type { PlatformFacet, Tag } from '../../api/collection'
import type { ListState } from '../../lib/listParams'
import { CONDITIONS, ITEM_TYPES, PACKAGINGS, REGIONS, STATUSES } from '../../lib/listParams'

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

// FilterBar renders only the seven chip fieldsets now - sort, order,
// group, and Clear filters moved to ListControls (the always-visible
// controls row above this disclosure). Collection.tsx mounts this
// component only while its Filters toggle is open.
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
    </section>
  )
}
