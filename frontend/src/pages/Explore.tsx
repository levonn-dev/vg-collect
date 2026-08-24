import { Trans, useLingui } from '@lingui/react/macro'
import { msg } from '@lingui/core/macro'
import type { MessageDescriptor } from '@lingui/core'
import { useInfiniteQuery, useQuery } from '@tanstack/react-query'
import { useEffect, useState } from 'react'
import type { ExploreSort } from '../api/social'
import { fetchExplore, searchUsers } from '../api/social'
import EmptyState from '../components/EmptyState'
import LoadMoreButton from '../components/LoadMoreButton'
import ShelfCard from '../components/social/ShelfCard'
import UserChip from '../components/social/UserChip'
import Tabs, { type Tab } from '../components/Tabs'
import { parameters } from '../gen/facets'
import { pathsApiExploreGetParametersQuerySortValues } from '../api/schema'
import { refetchWarning, renderQueryState } from '../lib/queryBoundary'
import { tabButtonId } from '../lib/tabs'

const EXPLORE_SORTS = pathsApiExploreGetParametersQuerySortValues
const USER_SEARCH_Q_MAX = parameters.userSearchQ.maxLength

// Tabs.tsx renders whatever label string each caller hands it (no
// i18n awareness of its own - see components/Tabs.tsx); the table
// stays msg descriptors at module scope (same shape as SearchPicker's
// kindLabels) and gets resolved into the plain strings Tab<T> expects
// down in the component body, where i18n is available. Labels keyed
// by the generated EXPLORE_SORTS values rather than a second
// hand-typed key list.
const sortLabels: Record<ExploreSort, MessageDescriptor> = {
  recent: msg`Recent`,
  top: msg`Top`,
}
const PANEL_IDS: Record<ExploreSort, string> = {
  recent: 'explore-recent-panel',
  top: 'explore-top-panel',
}
const SORT_TABS: { key: ExploreSort; label: MessageDescriptor; panelId: string }[] =
  EXPLORE_SORTS.map((key) => ({ key, label: sortLabels[key], panelId: PANEL_IDS[key] }))

// The pause after the last keystroke before /api/search/users fires -
// long enough that ordinary typing never triggers a call per letter.
const SEARCH_DEBOUNCE_MS = 300
const SEARCH_MIN_CHARS = 2

// Explore is the discovery surface: a people-search box (its own
// debounced query, independent of the tabs below) over a shelf
// browser with two sorts. recent pages the full listed-shelf stream
// newest-first (worklist idiom: useInfiniteQuery + a Load more
// button); top is social's fixed all-time leaderboard, no deeper
// page. Only the active tab's query is enabled, so switching tabs
// fires exactly one new request, not two on mount.
export default function Explore() {
  const { t, i18n } = useLingui()
  const [tab, setTab] = useState<ExploreSort>('recent')
  const [searchText, setSearchText] = useState('')
  const [query, setQuery] = useState('')

  useEffect(() => {
    const timer = setTimeout(() => setQuery(searchText.trim()), SEARCH_DEBOUNCE_MS)
    return () => clearTimeout(timer)
  }, [searchText])

  const search = useQuery({
    queryKey: ['userSearch', query],
    queryFn: () => searchUsers(query),
    enabled: query.length >= SEARCH_MIN_CHARS,
  })

  const recent = useInfiniteQuery({
    queryKey: ['explore', 'recent'],
    queryFn: ({ pageParam }) => fetchExplore('recent', pageParam),
    initialPageParam: 0,
    getNextPageParam: (last) => last.next_offset ?? undefined,
    enabled: tab === 'recent',
  })

  const top = useQuery({
    queryKey: ['explore', 'top'],
    queryFn: () => fetchExplore('top'),
    enabled: tab === 'top',
  })

  const active = tab === 'recent' ? recent : top
  const shelves = tab === 'recent' ? recent.data?.pages.flatMap((p) => p.shelves) : top.data?.shelves

  return (
    <main aria-label={t`Explore`} className="py-6">
      <h2 className="mb-4 text-2xl font-bold"><Trans>Explore</Trans></h2>

      <div className="max-w-sm">
        <input
          type="search"
          aria-label={t`Search for people`}
          value={searchText}
          onChange={(e) => setSearchText(e.target.value)}
          placeholder={t`Search for people...`}
          maxLength={USER_SEARCH_Q_MAX}
          className="w-full rounded border border-gray-300 px-3 py-1.5 text-sm"
        />
      </div>
      {query.length >= SEARCH_MIN_CHARS && (
        <div className="mt-2 max-w-sm">
          {search.isPending && <p className="text-sm text-gray-500"><Trans>Searching...</Trans></p>}
          {search.isError && (
            <p role="alert" className="text-sm text-red-700">
              <Trans>Search is not working right now. Please try again.</Trans>
            </p>
          )}
          {search.isSuccess && search.data.length === 0 && (
            <p className="text-sm text-gray-500"><Trans>No people found for "{query}".</Trans></p>
          )}
          {search.isSuccess && search.data.length > 0 && (
            <ul aria-label={t`Search results`} className="flex flex-col gap-2">
              {search.data.map((p) => (
                <li key={p.user_id}>
                  <UserChip profile={p} />
                </li>
              ))}
            </ul>
          )}
        </div>
      )}

      <Tabs
        label={t`Explore sort`}
        tabs={SORT_TABS.map((s): Tab<ExploreSort> => ({ key: s.key, label: i18n._(s.label), panelId: s.panelId }))}
        active={tab}
        onChange={setTab}
        className="mt-6"
      />

      <div role="tabpanel" id={PANEL_IDS[tab]} aria-labelledby={tabButtonId(PANEL_IDS[tab])}>
        {refetchWarning(active)}
        {renderQueryState(active, {
          size: 'subsection',
          className: 'mt-4',
          role: 'alert',
          loading: <Trans>Loading shelves...</Trans>,
          error: <Trans>Shelves cannot be loaded right now. Please try again.</Trans>,
        }) ?? (
          shelves && (
            shelves.length === 0 ? (
              <EmptyState size="default"><Trans>No shared shelves yet.</Trans></EmptyState>
            ) : (
              <>
                <ul className="mt-4 grid grid-cols-1 gap-4 sm:grid-cols-2 lg:grid-cols-3">
                  {shelves.map((s) => (
                    <li key={s.id}>
                      <ShelfCard card={s} />
                    </li>
                  ))}
                </ul>
                {tab === 'recent' && <LoadMoreButton query={recent} className="mt-4" />}
              </>
            )
          )
        )}
      </div>
    </main>
  )
}
