import { Plural, Trans, useLingui } from '@lingui/react/macro'
import { useInfiniteQuery, useQuery } from '@tanstack/react-query'
import { useParams } from 'react-router'
import { ApiError } from '../api/client'
import { fetchShelfEntries, fetchShelfPage } from '../api/social'
import { formatDate } from '../lib/format'
import { offsetNextPageParam } from '../lib/pagination'
import { refetchWarning, renderQueryState } from '../lib/queryBoundary'
import EmptyState from '../components/EmptyState'
import EntryGroupSection from '../components/EntryGroupSection'
import LoadMoreButton from '../components/LoadMoreButton'
import CompactList from '../components/collection/CompactList'
import CoverGrid from '../components/collection/CoverGrid'
import EntryTable from '../components/collection/EntryTable'
import CommentComposer from '../components/social/CommentComposer'
import CommentList from '../components/social/CommentList'
import LikeButton from '../components/social/LikeButton'
import NotFoundState from '../components/social/NotFoundState'
import UserChip from '../components/social/UserChip'
import { foldHandle } from '../lib/handle'

type EntriesPage = Awaited<ReturnType<typeof fetchShelfEntries>>
type EntryGroupRow = NonNullable<EntriesPage['groups']>[number]

// mergeGroups accumulates infinite-query pages by group key: the
// server pages the underlying entry sequence FIRST, then partitions
// only that page's own window into groups (ListSharedShelfEntries
// slices before grouping) - so the same group key can recur across
// pages, and a "Load more" click can continue a group the previous
// page already started.
function mergeGroups(pages: EntriesPage[]): EntryGroupRow[] {
  const order: string[] = []
  const byKey = new Map<string, EntryGroupRow>()
  for (const page of pages) {
    for (const g of page.groups ?? []) {
      const existing = byKey.get(g.key)
      if (existing) {
        existing.entries = existing.entries.concat(g.entries)
      } else {
        byKey.set(g.key, { key: g.key, label: g.label, entries: [...g.entries] })
        order.push(g.key)
      }
    }
  }
  return order.map((k) => byKey.get(k)!)
}

function viewMode(params: Record<string, unknown>): 'table' | 'grid' | 'compact' {
  return params.mode === 'grid' || params.mode === 'compact' ? params.mode : 'table'
}

// SharedShelf is the public /u/:handle/shelves/:slug page: a
// read-only entry listing reusing EntryTable/CoverGrid/CompactList
// through their linkTo suppression (no route into /entries/:id - the
// viewer does not own these rows) plus a like button and a comment
// section. The query key folds both handle and slug with the same
// foldHandle (their fold expressions share the same shape:
// lower-and-strip-underscores) purely for cache-key stability - the
// path itself carries the raw typed values, and the server remains
// authoritative on identity. 404 covers unknown, private-owner, and
// private-shelf alike (no existence oracle).
export default function SharedShelf() {
  const { t } = useLingui()
  const { handle = '', slug = '' } = useParams()
  const page = useQuery({
    queryKey: ['sharedShelf', foldHandle(handle), foldHandle(slug)],
    queryFn: () => fetchShelfPage(handle, slug),
  })
  const shelfId = page.data?.shelf.id
  const entries = useInfiniteQuery({
    queryKey: ['sharedEntries', shelfId],
    queryFn: ({ pageParam }) => fetchShelfEntries(shelfId!, pageParam),
    initialPageParam: 0,
    getNextPageParam: (last, pages) =>
      offsetNextPageParam(
        last,
        pages,
        (p) => p.entries?.length ?? p.groups?.reduce((m, g) => m + g.entries.length, 0) ?? 0,
      ),
    enabled: shelfId !== undefined,
  })

  if (page.isPending || (page.isError && page.data === undefined)) {
    return renderQueryState(page, {
      size: 'page',
      role: 'alert',
      loading: <Trans>Loading shelf...</Trans>,
      error: <Trans>This shelf cannot be loaded right now. Please try again.</Trans>,
      notFound: page.isError && page.error instanceof ApiError && page.error.status === 404
        ? <NotFoundState />
        : undefined,
    })
  }

  const { shelf, owner, social_available, social } = page.data
  const pages = entries.data?.pages ?? []
  const flatEntries = pages.flatMap((p) => p.entries ?? [])
  const isGrouped = pages.some((p) => p.groups)
  const groups = isGrouped ? mergeGroups(pages) : []
  const totalEntries = pages[0]?.total_count
  const mode = viewMode(shelf.params)
  const View = mode === 'grid' ? CoverGrid : mode === 'compact' ? CompactList : EntryTable

  return (
    <main aria-label={t`Shared shelf`} className="py-6">
      {refetchWarning(page)}
      <header className="mb-6 border-b border-gray-200 pb-4">
        <h2 className="text-2xl font-bold">{shelf.name}</h2>
        <div className="mt-2 flex flex-wrap items-center gap-3">
          <UserChip profile={owner} />
          {social_available && social && (
            <LikeButton shelfId={shelf.id} viewerLikes={social.viewer_likes} count={social.like_count} />
          )}
          {totalEntries !== undefined && (
            <span className="text-sm text-gray-500">
              <Plural value={totalEntries} one="# entry" other="# entries" />
            </span>
          )}
          {shelf.published_at && (
            <span className="text-sm text-gray-500">{formatDate(shelf.published_at)}</span>
          )}
        </div>
      </header>

      <section aria-label={t`Entries`} className="mb-8">
        {refetchWarning(entries)}
        {renderQueryState(entries, {
          size: 'subsection',
          role: 'alert',
          loading: <Trans>Loading entries...</Trans>,
          error: <Trans>Entries cannot be loaded right now. Please try again.</Trans>,
        }) ?? (
          flatEntries.length === 0 && groups.length === 0 ? (
            <EmptyState size="compact"><Trans>This shelf is empty.</Trans></EmptyState>
          ) : (
            <>
              {groups.length > 0 ? (
                groups.map((g) => (
                  <EntryGroupSection key={g.key} label={g.label}>
                    {mode === 'table' ? (
                      <EntryTable
                        entries={g.entries}
                        linkTo={() => null}
                        numbered={shelf.params.sort === 'backlog_rank'}
                        shared
                      />
                    ) : (
                      <View entries={g.entries} linkTo={() => null} shared />
                    )}
                  </EntryGroupSection>
                ))
              ) : mode === 'table' ? (
                <EntryTable
                  entries={flatEntries}
                  linkTo={() => null}
                  numbered={shelf.params.sort === 'backlog_rank'}
                  shared
                />
              ) : (
                <View entries={flatEntries} linkTo={() => null} shared />
              )}
              <LoadMoreButton query={entries} className="mt-4" />
            </>
          )
        )}
      </section>

      <CommentComposer shelfId={shelf.id} />
      {/* Keyed on shelf.id so a client-side navigation to a different
          shelf (no route remount otherwise - same route pattern) always
          unmounts this instance rather than reusing it: useCommentDelete's
          pending-undo flush is meant to fire only on a true unmount. */}
      <CommentList key={shelf.id} shelfId={shelf.id} ownerId={owner.user_id} />
    </main>
  )
}
