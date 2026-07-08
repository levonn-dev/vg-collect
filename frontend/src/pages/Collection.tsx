import { keepPreviousData, useQuery } from '@tanstack/react-query'
import { Link, useSearchParams } from 'react-router'
import type { Entry } from '../api/collection'
import { fetchEntries, fetchPlatformFacets, fetchTags } from '../api/collection'
import BacklogBoard from '../components/collection/BacklogBoard'
import CompactList from '../components/collection/CompactList'
import CoverGrid from '../components/collection/CoverGrid'
import EntryTable from '../components/collection/EntryTable'
import FilterBar from '../components/collection/FilterBar'
import Pager from '../components/collection/Pager'
import PinStar from '../components/collection/PinStar'
import ViewPicker from '../components/collection/ViewPicker'
import InsightsPanel from '../components/insights/InsightsPanel'
import type { ListState } from '../lib/listParams'
import { fromSearchParams, toQuery, toSearchParams } from '../lib/listParams'

export default function Collection() {
  const [searchParams, setSearchParams] = useSearchParams()
  const state = fromSearchParams(searchParams)
  const apply = (next: ListState) => setSearchParams(toSearchParams(next))

  const query = toQuery(state)
  const list = useQuery({
    queryKey: ['entries', query.toString()],
    queryFn: () => fetchEntries(query),
    placeholderData: keepPreviousData,
  })
  const platforms = useQuery({ queryKey: ['platform-facets'], queryFn: fetchPlatformFacets })
  const tags = useQuery({ queryKey: ['tags'], queryFn: fetchTags })

  if (list.isPending) return <main className="py-8">Loading collection...</main>
  if (list.isError) {
    return (
      <main className="py-8" role="alert">
        The collection cannot be loaded right now. Please try again.
      </main>
    )
  }

  const { entries = [], groups, total_count, pricing_available } = list.data
  const View = state.mode === 'grid' ? CoverGrid : state.mode === 'compact' ? CompactList : EntryTable
  const pinSlot = (e: Entry) => <PinStar entry={e} />
  // Distinguishes "your collection is empty" from "these filters match
  // nothing": mode and page are normalized away since neither is a filter.
  const filtered = toSearchParams({ ...state, mode: 'table', page: 0 }).size > 0
  return (
    <main className="py-6" aria-label="Collection">
      <ViewPicker state={state} onApply={apply} />
      <div className="mb-3 flex gap-1" role="group" aria-label="View mode">
        {(['table', 'grid', 'compact'] as const).map((m) => (
          <button
            key={m}
            aria-pressed={state.mode === m}
            onClick={() => apply({ ...state, mode: m })}
            className={
              state.mode === m
                ? 'rounded bg-gray-900 px-2 py-1 text-xs text-white'
                : 'rounded border border-gray-300 px-2 py-1 text-xs text-gray-600 hover:bg-gray-50'
            }
          >
            {m === 'table' ? 'Table' : m === 'grid' ? 'Covers' : 'Compact'}
          </button>
        ))}
      </div>
      <FilterBar
        state={state}
        platforms={platforms.data ?? []}
        tags={tags.data ?? []}
        onChange={(next) => apply({ ...next, page: 0 })}
      />
      <InsightsPanel state={state} />
      {!pricing_available && (
        <p role="alert" className="mb-4 rounded bg-amber-50 p-3 text-sm text-amber-800">
          Market pricing is temporarily unavailable; values are hidden.
        </p>
      )}
      {total_count === 0 ? (
        filtered ? (
          <p className="py-12 text-center text-gray-500">Nothing matches these filters.</p>
        ) : (
          <p className="py-12 text-center text-gray-500">
            Nothing here yet. <Link to="/add" className="underline">Add your first item.</Link>
          </p>
        )
      ) : state.sort === 'backlog_rank' && !groups ? (
        <BacklogBoard entries={entries} />
      ) : groups ? (
        groups.map((g) => (
          <section key={g.key} aria-label={g.label} className="mb-6">
            <h3 className="mb-1 text-sm font-semibold uppercase tracking-wide text-gray-500">
              {g.label}
            </h3>
            <View entries={g.entries} pinSlot={pinSlot} />
          </section>
        ))
      ) : (
        <View entries={entries} pinSlot={pinSlot} />
      )}
      <Pager page={state.page} totalCount={total_count} onPage={(p) => apply({ ...state, page: p })} />
    </main>
  )
}
