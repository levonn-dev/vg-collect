import { Trans, useLingui } from '@lingui/react/macro'
import { plural } from '@lingui/core/macro'
import { keepPreviousData, useQuery } from '@tanstack/react-query'
import { useState } from 'react'
import { Link, useSearchParams } from 'react-router'
import type { Entry } from '../api/collection'
import { fetchEntries, fetchTags } from '../api/collection'
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
import EmptyState from '../components/EmptyState'
import EntryGroupSection from '../components/EntryGroupSection'
import { fetchEntryFacets } from '../lib/entryFacets'
import type { ListState } from '../lib/listParams'
import { fromSearchParams, lastPage, toQuery, toSearchParams } from '../lib/listParams'
import { refetchWarning, renderQueryState } from '../lib/queryBoundary'
import { tabButtonId } from '../lib/tabs'
import { useDocumentTitle } from '../lib/useDocumentTitle'

type CollectionTab = 'items' | 'shelves'

const ITEMS_PANEL = 'collection-items-panel'
const SHELVES_PANEL = 'collection-shelves-panel'

export default function Collection() {
  const { t } = useLingui()
  useDocumentTitle(t`Collection`)
  const collectionTabs: Tab<CollectionTab>[] = [
    { key: 'items', label: t`Items`, panelId: ITEMS_PANEL },
    { key: 'shelves', label: t`Shelves`, panelId: SHELVES_PANEL },
  ]
  const [searchParams, setSearchParams] = useSearchParams()
  const state = fromSearchParams(searchParams)
  const apply = (next: ListState) => setSearchParams(toSearchParams(next))
  const onFilterChange = (next: ListState) => apply({ ...next, page: 0 })
  // Local UI state, not part of ListState: never persists, so applying
  // a shelf never forces the panel open.
  const [filtersOpen, setFiltersOpen] = useState(false)
  // Local, not URL-driven (matches Feed/Admin); Items panel remounts
  // each switch, but filtersOpen lives on Collection and survives it.
  const [tab, setTab] = useState<CollectionTab>('items')

  // bulkMode gates the toggle/bar/checkboxes; selected is shared across
  // every grouped EntryTable via this one Set. Neither persists to the URL.
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
  // Leaving table mode drops bulk selection rather than silently
  // resuming it later. Adjusted during render (React's reset pattern),
  // not an effect, to avoid an extra pass.
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

  if (list.isPending || (list.isError && list.data === undefined)) {
    return renderQueryState(list, {
      size: 'page',
      role: 'alert',
      loading: <Trans>Loading collection...</Trans>,
      error: <Trans>The collection cannot be loaded right now. Please try again.</Trans>,
    })
  }

  const { entries = [], groups, total_count, pricing_available } = list.data
  const View = state.mode === 'grid' ? CoverGrid : state.mode === 'compact' ? CompactList : EntryTable
  const pinSlot = (e: Entry) => <PinStar entry={e} />
  // Distinguishes empty collection from filters matching nothing;
  // mode/page are normalized away first.
  const filtered = toSearchParams({ ...state, mode: 'table', page: 0 }).size > 0
  // Board has no row checkboxes, so bulk mode has nothing to attach to
  // while it shows; also passed as bulkAvailable so the toggle itself
  // disappears (not just the bar), avoiding aria-pressed with no UI.
  // bulkMode/selected stay untouched, so an in-progress bulk edit
  // pauses and resumes once the board goes away.
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
    <main id="main-content" tabIndex={-1} className="py-6" aria-label={t`Collection`}>
      <Tabs label={t`Collection sections`} tabs={collectionTabs} active={tab} onChange={setTab} className="mb-4" />
      {tab === 'items' ? (
        <div role="tabpanel" id={ITEMS_PANEL} aria-labelledby={tabButtonId(ITEMS_PANEL)}>
          {refetchWarning(list)}
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
          {/* Always mounted (empty when nothing to say): announces more
              reliably than inserting on demand (see CopyButton). */}
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
                setBulkAnnouncement(plural(n, { one: 'Updated # entry.', other: 'Updated # entries.' }))
              }}
            />
          )}
          {total_count === 0 ? (
            filtered ? (
              <EmptyState size="default"><Trans>Nothing matches these filters.</Trans></EmptyState>
            ) : (
              <EmptyState size="default">
                <Trans>
                  Nothing here yet. <Link to="/add" className="underline">Add your first item.</Link>
                </Trans>
              </EmptyState>
            )
          ) : !groups && entries.length === 0 ? (
            // total_count is real but this page has nothing: a stale
            // bookmark to a page number that shrank.
            <EmptyState size="default">
              <Trans>
                This page is past the end of your list.{' '}
                <button
                  type="button"
                  onClick={() => apply({ ...state, page: lastPage(total_count) })}
                  className="underline"
                >
                  Go to the last page.
                </button>
              </Trans>
            </EmptyState>
          ) : state.sort === 'backlog_rank' && !groups ? (
            <BacklogBoard entries={entries} page={state.page} totalCount={total_count} />
          ) : groups ? (
            groups.map((g) => (
              <EntryGroupSection key={g.key} label={g.label}>
                {renderEntries(g.entries)}
              </EntryGroupSection>
            ))
          ) : (
            renderEntries(entries)
          )}
          <Pager page={state.page} totalCount={total_count} onPage={(p) => apply({ ...state, page: p })} />
        </div>
      ) : (
        <div role="tabpanel" id={SHELVES_PANEL} aria-labelledby={tabButtonId(SHELVES_PANEL)}>
          <ShelfManager />
        </div>
      )}
    </main>
  )
}
