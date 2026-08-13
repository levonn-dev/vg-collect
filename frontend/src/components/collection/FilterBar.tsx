import { Trans, useLingui } from '@lingui/react/macro'
import { msg } from '@lingui/core/macro'
import type { MessageDescriptor } from '@lingui/core'
import type { PlatformFacet, Tag } from '../../api/collection'
import type { ListState } from '../../lib/listParams'
import { CONDITIONS, ITEM_TYPES, PACKAGINGS, REGIONS, STATUSES } from '../../lib/listParams'
import SectionLabel from '../SectionLabel'

const chipLabels: Record<string, MessageDescriptor> = {
  backlog: msg`Backlog`, playing: msg`Playing`, beaten: msg`Beaten`, completed: msg`Completed`,
  dropped: msg`Dropped`, shelved: msg`Shelved`,
  game: msg`Games`, console: msg`Consoles`, accessory: msg`Accessories`,
  sealed: msg`Sealed`, cib: msg`CIB`, loose: msg`Loose`,
  ntsc_u: msg`NTSC-U`, ntsc_j: msg`NTSC-J`, pal: msg`PAL`, korea: msg`Korea`,
  brazil: msg`Brazil`, china: msg`China`, region_free: msg`Region free`,
  mint: msg`Mint`, near_mint: msg`Near mint`, very_good: msg`Very good`, good: msg`Good`,
  acceptable: msg`Acceptable`, poor: msg`Poor`,
}

interface FilterBarProps {
  state: ListState
  platforms: PlatformFacet[]
  tags: Tag[]
  // Distinct credit names across the collection (fetchEntryFacets),
  // same derivation as platforms.
  developers?: string[]
  publishers?: string[]
  onChange: (next: ListState) => void
}

// FilterBar renders only the seven chip fieldsets now - sort, order,
// group, and Clear filters moved to ListControls (the always-visible
// controls row above this disclosure). Collection.tsx mounts this
// component only while its Filters toggle is open.
export default function FilterBar({ state, platforms, tags, developers = [], publishers = [], onChange }: FilterBarProps) {
  const { t, i18n } = useLingui()
  function toggled<T>(list: T[], v: T): T[] {
    return list.includes(v) ? list.filter((x) => x !== v) : [...list, v]
  }

  // Credit fieldsets render facet names verbatim (open-world values,
  // never translated), the platform fieldset's shape with a string
  // dimension.
  const nameGroup = (legend: string, all: string[], key: 'developer' | 'publisher', empty: string) => (
    <fieldset className="flex flex-wrap items-center gap-2">
      <SectionLabel as="legend" size="xs" className="float-left mr-2">
        {legend}
      </SectionLabel>
      {all.map((v) => (
        <label key={v} className="flex items-center gap-1 text-sm">
          <input
            type="checkbox"
            checked={state[key].includes(v)}
            onChange={() => onChange({ ...state, [key]: toggled(state[key], v) })}
          />
          {v}
        </label>
      ))}
      {all.length === 0 && <span className="text-xs text-gray-400">{empty}</span>}
    </fieldset>
  )

  const chipGroup = <T extends string>(legend: string, all: readonly T[], key: keyof ListState) => (
    <fieldset className="flex flex-wrap items-center gap-2">
      <SectionLabel as="legend" size="xs" className="float-left mr-2">
        {legend}
      </SectionLabel>
      {all.map((v) => (
        <label key={v} className="flex items-center gap-1 text-sm">
          <input
            type="checkbox"
            checked={(state[key] as T[]).includes(v)}
            onChange={() => onChange({ ...state, [key]: toggled(state[key] as T[], v) })}
          />
          {chipLabels[v] ? i18n._(chipLabels[v]) : v}
        </label>
      ))}
    </fieldset>
  )

  return (
    <section aria-label={t`Filters`} className="mb-4 flex flex-col gap-2 rounded border border-gray-200 p-3">
      {chipGroup(t`Status`, STATUSES, 'status')}
      {chipGroup(t`Type`, ITEM_TYPES, 'itemType')}
      {chipGroup(t`Packaging`, PACKAGINGS, 'packaging')}
      {chipGroup(t`Region`, REGIONS, 'region')}
      {chipGroup(t`Condition`, CONDITIONS, 'itemCondition')}
      <fieldset className="flex flex-wrap items-center gap-2">
        <SectionLabel as="legend" size="xs" className="float-left mr-2">
          <Trans>Platform</Trans>
        </SectionLabel>
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
        {platforms.length === 0 && <span className="text-xs text-gray-400"><Trans>No platforms yet</Trans></span>}
      </fieldset>
      {nameGroup(t`Developer`, developers, 'developer', t`No developers yet`)}
      {nameGroup(t`Publisher`, publishers, 'publisher', t`No publishers yet`)}
      <fieldset className="flex flex-wrap items-center gap-2">
        <SectionLabel as="legend" size="xs" className="float-left mr-2">
          <Trans>Tags (all of)</Trans>
        </SectionLabel>
        {tags.map((tag) => (
          <label key={tag.id} className="flex items-center gap-1 text-sm">
            <input
              type="checkbox"
              checked={state.tagId.includes(tag.id)}
              onChange={() => onChange({ ...state, tagId: toggled(state.tagId, tag.id) })}
            />
            {tag.name}
          </label>
        ))}
        {tags.length === 0 && <span className="text-xs text-gray-400"><Trans>No tags yet</Trans></span>}
      </fieldset>
    </section>
  )
}
