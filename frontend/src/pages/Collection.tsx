import { Trans, useLingui } from '@lingui/react/macro'
import { keepPreviousData, useQuery } from '@tanstack/react-query'
import { useState } from 'react'
import { Link, useSearchParams } from 'react-router'
import type { Entry } from '../api/collection'
import { fetchEntries, fetchEntryFacets, fetchTags } from '../api/collection'
import BacklogBoard from '../components/collection/BacklogBoard'
import BulkEditBar from '../components/collection/BulkEditBar'
import CompactList from '../components/collection/CompactList'
import CoverGrid from '../components/collection/CoverGrid'
import EntryTable from '../components/collection/EntryTable'
import FilterBar from '../components/collection/FilterBar'
import ListControls from '../components/collection/ListControls'
import Pager from '../components/collection/Pager'
import PinStar from '../components/collection/PinStar'
import ShelfManager from '../components/collection/ShelfManager'
import ViewPicker from '../components/collection/ViewPicker'
import InsightsPanel from '../components/insights/InsightsPanel'
import Tabs, { type Tab } from '../components/Tabs'
import type { ListState } from '../lib/listParams'
import { fromSearchParams, toQuery, toSearchParams } from '../lib/listParams'

type CollectionTab = 'items' | 'shelves'

export default function Collection() {
  const { t } = useLingui()
  const collectionTabs: Tab<CollectionTab>[] = [
    { key: 'items', label: t`Items` },
    { key: 'shelves', label: t`Shelves` },
  ]
  const [searchParams, setSearchParams] = useSearchParams()
  const state = fromSearchParams(searchParams)
  const apply = (next: ListState) => setSearchParams(toSearchParams(next))
  const onFilterChange = (next: ListState) => apply({ ...next, page: 0 })
  // Filter-panel visibility is local UI state, not part of ListState: it
  // never persists to the URL or a saved shelf, so applying a shelf (or
  // loading a URL) that carries filters never forces the panel open -
  // the Filters count badge is the only signal for that.
  const [filtersOpen, setFiltersOpen] = useState(false)
  // Tab state is local, not URL-driven (matches Feed/Admin). The Items
  // panel's own contents remount on every switch back to it (plain
  // conditional rendering, same as Feed/Admin), but filtersOpen above
  // lives on Collection itself, which never unmounts on a tab switch,
  // so it survives a round trip - acceptable either way.
  const [tab, setTab] = useState<CollectionTab>('items')

  // Bulk edit's own local state: bulkMode gates the toggle's pressed
  // state and the bar/checkboxes; selected is shared across every
  // table on screen (grouped rendering mounts one EntryTable per
  // group, all pointed at this same Set). Like filtersOpen above,
  // neither persists to the URL and both survive a tab round trip
  // since they live on Collection itself.
  const [bulkMode, setBulkMode] = useState(false)
  const [selected, setSelected] = useState<Set<string>>(new Set())
  const [bulkAnnouncement, setBulkAnnouncement] = useState('')
  const toggleBulkMode = () => {
    setBulkMode((on) => !on)
    setSelected(new Set())
    setBulkAnnouncement('')
  }
  const toggleSelect = (id: string) => {
    setSelected((prev) => {
      const next = new Set(prev)
      if (next.has(id)) next.delete(id)
      else next.add(id)
      return next
    })
  }
  // Leaving table mode - by the mode buttons, a saved shelf, or
  // browser back/forward - exits bulk mode and drops the selection
  // rather than leaving it to silently resume on a later return trip.
  // Adjusted during render (React's documented pattern for resetting
  // state when a value changes) instead of in an effect, so there is
  // no extra post-commit render pass just to correct the mode.
  const [modeAtLastRender, setModeAtLastRender] = useState(state.mode)
  if (state.mode !== modeAtLastRender) {
    setModeAtLastRender(state.mode)
    if (state.mode !== 'table') {
      setBulkMode(false)
      setSelected(new Set())
    }
  }

  const query = toQuery(state)
  const list = useQuery({
    queryKey: ['entries', query.toString()],
    queryFn: () => fetchEntries(query),
    placeholderData: keepPreviousData,
  })
  const facets = useQuery({ queryKey: ['entry-facets'], queryFn: fetchEntryFacets })
  const tags = useQuery({ queryKey: ['tags'], queryFn: fetchTags })

  if (list.isPending) return <main className="py-8"><Trans>Loading collection...</Trans></main>
  if (list.isError) {
    return (
      <main className="py-8" role="alert">
        <Trans>The collection cannot be loaded right now. Please try again.</Trans>
      </main>
    )
  }

  const { entries = [], groups, total_count, pricing_available } = list.data
  const View = state.mode === 'grid' ? CoverGrid : state.mode === 'compact' ? CompactList : EntryTable
  const pinSlot = (e: Entry) => <PinStar entry={e} />
  // Distinguishes "your collection is empty" from "these filters match
  // nothing": mode and page are normalized away since neither is a filter.
  const filtered = toSearchParams({ ...state, mode: 'table', page: 0 }).size > 0
  // The backlog drag board (below) takes over the table's own spot
  // whenever a pure-backlog filter is sorted by rank and ungrouped -
  // it has no row checkboxes, so bulk mode has nothing to attach to
  // while it is showing (state.mode can still say 'table' the whole
  // time; this is a second, independent reason to hide the bar). Also
  // passed to ListControls as bulkAvailable so the toggle itself
  // disappears along with the bar/checkboxes instead of flipping
  // aria-pressed with no visible bulk UI behind it; bulkMode/selected
  // are untouched by this (unlike an actual mode switch away from
  // table), so a bulk edit already in progress when the board takes
  // over stays paused, and leaving the board restores all three.
  const boardActive = state.sort === 'backlog_rank' && !groups
  const bulkActive = bulkMode && state.mode === 'table' && !boardActive
  const renderEntries = (items: Entry[]) =>
    state.mode === 'table' ? (
      <EntryTable
        entries={items}
        pinSlot={pinSlot}
        selectable={bulkActive}
        selected={selected}
        onToggleSelect={toggleSelect}
      />
    ) : (
      <View entries={items} pinSlot={pinSlot} />
    )
  return (
    <main className="py-6" aria-label={t`Collection`}>
      <Tabs label={t`Collection sections`} tabs={collectionTabs} active={tab} onChange={setTab} className="mb-4" />
      {tab === 'items' ? (
        <>
          <ViewPicker state={state} onApply={apply} />
          <ListControls
            state={state}
            onApply={apply}
            onChange={onFilterChange}
            filtersOpen={filtersOpen}
            onToggleFilters={() => setFiltersOpen((open) => !open)}
            bulkMode={bulkMode}
            onToggleBulk={toggleBulkMode}
            bulkAvailable={!boardActive}
          />
          {filtersOpen && (
            <FilterBar
              state={state}
              platforms={facets.data?.platforms ?? []}
              tags={tags.data ?? []}
              developers={facets.data?.developers ?? []}
              publishers={facets.data?.publishers ?? []}
              onChange={onFilterChange}
            />
          )}
          <InsightsPanel state={state} />
          {!pricing_available && (
            <p role="alert" className="mb-4 rounded bg-amber-50 p-3 text-sm text-amber-800">
              <Trans>Market pricing is temporarily unavailable; values are hidden.</Trans>
            </p>
          )}
          {/* Always mounted, text empty when there is nothing to say - an
              always-mounted live region announces more reliably than one
              inserted only when it already has content (see CopyButton's
              own status sibling for the same reasoning). */}
          <p
            role="status"
            className={
              bulkAnnouncement ? 'mb-3 rounded bg-green-50 p-3 text-sm text-green-800' : 'sr-only'
            }
          >
            {bulkAnnouncement}
          </p>
          {bulkActive && (
            <BulkEditBar
              selected={selected}
              tags={tags.data ?? []}
              onCancel={() => {
                setBulkMode(false)
                setSelected(new Set())
              }}
              onApplied={(n) => {
                setBulkMode(false)
                setSelected(new Set())
                setBulkAnnouncement(t`Updated ${n} entries.`)
              }}
            />
          )}
          {total_count === 0 ? (
            filtered ? (
              <p className="py-12 text-center text-gray-500"><Trans>Nothing matches these filters.</Trans></p>
            ) : (
              <p className="py-12 text-center text-gray-500">
                <Trans>
                  Nothing here yet. <Link to="/add" className="underline">Add your first item.</Link>
                </Trans>
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
                {renderEntries(g.entries)}
              </section>
            ))
          ) : (
            renderEntries(entries)
          )}
          <Pager page={state.page} totalCount={total_count} onPage={(p) => apply({ ...state, page: p })} />
        </>
      ) : (
        <ShelfManager />
      )}
    </main>
  )
}
