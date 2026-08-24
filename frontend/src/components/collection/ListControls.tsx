import { Trans, useLingui } from '@lingui/react/macro'
import { msg } from '@lingui/core/macro'
import type { MessageDescriptor } from '@lingui/core'
import { parameters } from '../../gen/facets'
import { btnSecondary } from '../../lib/formStyles'
import type { GroupBy, ListState, Sort } from '../../lib/listParams'
import { canBacklogSort, defaultListState, GROUPS, SORTS } from '../../lib/listParams'

const DEFAULT_SORT = parameters.entriesSort.default

const sortLabels: Record<Sort, MessageDescriptor> = {
  name: msg`Name`,
  release_date: msg`Release date`,
  purchased_at: msg`Purchase date`,
  created_at: msg`Date added`,
  value: msg`Value`,
  paid: msg`Price paid`,
  rating: msg`Rating`,
  backlog_rank: msg`Backlog order`,
}

// defaultSortLabels: the "(default)" - suffixed sibling of sortLabels
// for the blank/no-sort option below, which mirrors whatever the
// server falls back to when sort is left off the request. Keyed by
// DEFAULT_SORT rather than assuming created_at by hand, so a generated
// default change lands here instead of silently going stale; a
// default with no dedicated phrasing yet falls back to its plain
// sortLabels entry.
const defaultSortLabels: Partial<Record<Sort, MessageDescriptor>> = {
  created_at: msg`Date added (default)`,
}

const groupLabels: Record<GroupBy, MessageDescriptor> = {
  platform: msg`Platform`,
  status: msg`Status`,
  item_type: msg`Item type`,
  location: msg`Location`,
  tag: msg`Tag`,
}

// The nine chip dimensions FilterBar renders behind the Filters
// disclosure. Sort/order/group/mode/page are list-shape controls, not
// filters, so they never count toward the badge even though sort and
// group also drive Clear filters' visibility below.
const FILTER_DIMENSIONS = [
  'status', 'itemType', 'packaging', 'region', 'developer', 'publisher', 'itemCondition', 'platformId', 'tagId',
] as const satisfies readonly (keyof ListState)[]

function activeFilterCount(state: ListState): number {
  return FILTER_DIMENSIONS.filter((key) => state[key].length > 0).length
}

interface ListControlsProps {
  state: ListState
  // Mode changes bypass the page reset that filter/sort/group changes
  // get (mode never changes which entries match), exactly as Collection
  // wired the standalone mode toggle before this row absorbed it - so
  // this takes the raw setter, not the page-resetting onChange below.
  onApply: (next: ListState) => void
  onChange: (next: ListState) => void
  filtersOpen: boolean
  onToggleFilters: () => void
  // Bulk edit only makes sense over the table view's row furniture
  // (grid/compact have none, and the backlog drag board is its own
  // surface with no checkboxes either) - Collection owns the mode
  // state itself and gates rendering, this row only shows the toggle.
  bulkMode: boolean
  onToggleBulk: () => void
  // Collection passes !boardActive: the backlog drag board takes over
  // the table's own spot (independent of display mode) and has no row
  // checkboxes, so a toggle that flips aria-pressed with no visible
  // bulk UI behind it would be misleading. Bulk mode already active
  // when the board takes over stays paused underneath, unaffected by
  // this - the button just disappears along with the bar/checkboxes,
  // and leaving the board restores all three together.
  bulkAvailable: boolean
}

export default function ListControls({
  state, onApply, onChange, filtersOpen, onToggleFilters, bulkMode, onToggleBulk, bulkAvailable,
}: ListControlsProps) {
  const { t, i18n } = useLingui()
  const sorts = SORTS.filter((s) => s !== 'backlog_rank' || canBacklogSort(state))
  const filterCount = activeFilterCount(state)
  const canClear = filterCount > 0 || !!state.sort || !!state.order || !!state.groupBy
  const direction = (state.order ?? 'desc') === 'desc' ? t`descending` : t`ascending`

  return (
    <div className="mb-3 flex flex-wrap items-center gap-3">
      <div className="flex gap-1" role="group" aria-label={t`Display mode`}>
        {(['table', 'grid', 'compact'] as const).map((m) => (
          <button
            key={m}
            type="button"
            aria-pressed={state.mode === m}
            onClick={() => onApply({ ...state, mode: m })}
            className={
              state.mode === m
                ? 'rounded bg-gray-900 px-2 py-1 text-xs text-white'
                : 'rounded border border-gray-300 px-2 py-1 text-xs text-gray-600 hover:bg-gray-50'
            }
          >
            {m === 'table' ? t`Table` : m === 'grid' ? t`Covers` : t`Compact`}
          </button>
        ))}
      </div>
      {state.mode === 'table' && bulkAvailable && (
        <button
          type="button"
          aria-pressed={bulkMode}
          onClick={onToggleBulk}
          className={
            bulkMode
              ? 'rounded bg-gray-900 px-2 py-1 text-sm text-white'
              : 'rounded border border-gray-300 px-2 py-1 text-sm text-gray-600 hover:bg-gray-50'
          }
        >
          <Trans>Bulk edit</Trans>
        </button>
      )}
      <label className="flex items-center gap-2 text-sm font-medium">
        <Trans>Sort</Trans>
        <select
          value={state.sort ?? ''}
          onChange={(e) =>
            onChange({ ...state, sort: e.target.value === '' ? undefined : (e.target.value as Sort) })
          }
          className="rounded border border-gray-300 px-2 py-1 text-sm"
        >
          <option value="">{i18n._(defaultSortLabels[DEFAULT_SORT] ?? sortLabels[DEFAULT_SORT])}</option>
          {sorts.map((s) => (
            <option key={s} value={s}>
              {i18n._(sortLabels[s])}
            </option>
          ))}
        </select>
      </label>
      <button
        type="button"
        onClick={() => onChange({ ...state, order: state.order === 'asc' ? 'desc' : 'asc' })}
        className={btnSecondary}
      >
        <Trans>Order: {direction}</Trans>
      </button>
      <label className="flex items-center gap-2 text-sm font-medium">
        <Trans>Group by</Trans>
        <select
          value={state.groupBy ?? ''}
          onChange={(e) =>
            onChange({ ...state, groupBy: e.target.value === '' ? undefined : (e.target.value as GroupBy) })
          }
          className="rounded border border-gray-300 px-2 py-1 text-sm"
        >
          <option value="">{t`Ungrouped`}</option>
          {GROUPS.map((g) => (
            <option key={g} value={g}>
              {i18n._(groupLabels[g])}
            </option>
          ))}
        </select>
      </label>
      <button
        type="button"
        onClick={onToggleFilters}
        aria-expanded={filtersOpen}
        className={btnSecondary}
      >
        {filterCount > 0 ? t`Filters (${filterCount})` : t`Filters`}
      </button>
      {canClear && (
        <button
          type="button"
          onClick={() => onChange({ ...defaultListState(), mode: state.mode })}
          className={`${btnSecondary} text-gray-600 ml-auto`}
        >
          <Trans>Clear filters</Trans>
        </button>
      )}
    </div>
  )
}
