import { useInfiniteQuery, useQuery } from '@tanstack/react-query'
import { useEffect, useState } from 'react'
import type { ExploreSort } from '../api/social'
import { fetchExplore, searchUsers } from '../api/social'
import ShelfCard from '../components/social/ShelfCard'
import UserChip from '../components/social/UserChip'
import Tabs, { type Tab } from '../components/Tabs'

const SORT_TABS: Tab<ExploreSort>[] = [
  { key: 'recent', label: 'Recent' },
  { key: 'top', label: 'Top' },
]

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
    <main aria-label="Explore" className="py-6">
      <h2 className="mb-4 text-2xl font-bold">Explore</h2>

      <div className="max-w-sm">
        <input
          type="search"
          aria-label="Search for people"
          value={searchText}
          onChange={(e) => setSearchText(e.target.value)}
          placeholder="Search for people..."
          maxLength={64}
          className="w-full rounded border border-gray-300 px-3 py-1.5 text-sm"
        />
      </div>
      {query.length >= SEARCH_MIN_CHARS && (
        <div className="mt-2 max-w-sm">
          {search.isPending && <p className="text-sm text-gray-500">Searching...</p>}
          {search.isError && (
            <p role="alert" className="text-sm text-red-700">
              Search is not working right now. Please try again.
            </p>
          )}
          {search.isSuccess && search.data.length === 0 && (
            <p className="text-sm text-gray-500">No people found for "{query}".</p>
          )}
          {search.isSuccess && search.data.length > 0 && (
            <ul aria-label="Search results" className="flex flex-col gap-2">
              {search.data.map((p) => (
                <li key={p.user_id}>
                  <UserChip profile={p} />
                </li>
              ))}
            </ul>
          )}
        </div>
      )}

      <Tabs label="Explore sort" tabs={SORT_TABS} active={tab} onChange={setTab} className="mt-6" />

      {active.isPending && <p className="mt-4 text-sm text-gray-500">Loading shelves...</p>}
      {active.isError && (
        <p role="alert" className="mt-4 text-sm text-red-700">
          Shelves cannot be loaded right now. Please try again.
        </p>
      )}
      {!active.isPending && !active.isError && shelves && (
        shelves.length === 0 ? (
          <p className="py-12 text-center text-gray-500">No shared shelves yet.</p>
        ) : (
          <>
            <ul className="mt-4 grid grid-cols-1 gap-4 sm:grid-cols-2 lg:grid-cols-3">
              {shelves.map((s) => (
                <li key={s.id}>
                  <ShelfCard card={s} />
                </li>
              ))}
            </ul>
            {tab === 'recent' && recent.hasNextPage && (
              <button
                type="button"
                onClick={() => void recent.fetchNextPage()}
                disabled={recent.isFetchingNextPage}
                className="mt-4 rounded border border-gray-300 px-3 py-1 text-sm hover:bg-gray-50 disabled:opacity-50"
              >
                Load more
              </button>
            )}
          </>
        )
      )}
    </main>
  )
}
