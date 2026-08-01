import { Trans, useLingui } from '@lingui/react/macro'
import { msg } from '@lingui/core/macro'
import type { MessageDescriptor } from '@lingui/core'
import { useInfiniteQuery } from '@tanstack/react-query'
import { useState } from 'react'
import { Link } from 'react-router'
import type { FeedItem, FeedTab, ShelfCard as ShelfCardData } from '../api/social'
import { fetchFeed } from '../api/social'
import Tabs, { type Tab } from '../components/Tabs'
import UserChip from '../components/social/UserChip'
import { relativeTime } from '../lib/relativeTime'

// Tabs.tsx renders whatever label string each caller hands it (no
// i18n awareness of its own - see components/Tabs.tsx); the table
// stays msg descriptors at module scope (same shape as Explore's
// SORT_TABS) and gets resolved into the plain strings Tab<T> expects
// down in the component body, where i18n is available.
const FEED_TABS: { key: FeedTab; label: MessageDescriptor }[] = [
  { key: 'following', label: msg`Following` },
  { key: 'you', label: msg`You` },
]

// Same module-table-of-descriptors idiom as FEED_TABS just above: verb
// labels are looked up by FeedRow at render time via i18n._(), not
// consumed as plain strings here.
const VERB_TEXT: Record<FeedItem['verb'], MessageDescriptor> = {
  followed_user: msg`followed`,
  liked_shelf: msg`liked`,
  commented_shelf: msg`commented on`,
  published_shelf: msg`published`,
}

function shelfHref(shelf: ShelfCardData): string {
  return `/u/${shelf.owner.handle}/shelves/${shelf.slug}`
}

// FeedRow renders one activity row by verb. actor always attaches;
// shelf/followed_user/comment_excerpt ride along only for the verbs
// whose payload carries them (see FeedItem) - the bff's fill loop
// drops a row entirely when its object fails the tab's gating rule
// rather than send a shelf verb with no shelf, but each branch still
// guards its field before use so a malformed row degrades to nothing
// instead of crashing the list. The excerpt clamps to two lines via
// line-clamp-2 (same idiom as CoverGrid's card titles).
function FeedRow({ item }: { item: FeedItem }) {
  const { i18n } = useLingui()
  const when = <span className="ml-auto shrink-0 text-xs text-gray-400">{relativeTime(item.created_at)}</span>

  if (item.verb === 'followed_user') {
    if (!item.followed_user) return null
    return (
      <li className="flex flex-wrap items-center gap-1.5 border-b border-gray-100 py-3 text-sm">
        <UserChip profile={item.actor} />
        <span className="text-gray-500">{i18n._(VERB_TEXT.followed_user)}</span>
        <UserChip profile={item.followed_user} />
        {when}
      </li>
    )
  }

  if (!item.shelf) return null
  const shelfLink = (
    <Link to={shelfHref(item.shelf)} className="font-medium hover:underline">
      {item.shelf.name}
    </Link>
  )

  if (item.verb === 'commented_shelf') {
    return (
      <li className="flex flex-col gap-1 border-b border-gray-100 py-3 text-sm">
        <div className="flex flex-wrap items-center gap-1.5">
          <UserChip profile={item.actor} />
          <span className="text-gray-500">{i18n._(VERB_TEXT.commented_shelf)}</span>
          {shelfLink}
          {when}
        </div>
        {item.comment_excerpt && (
          <p className="line-clamp-2 text-gray-600">{item.comment_excerpt}</p>
        )}
      </li>
    )
  }

  return (
    <li className="flex flex-wrap items-center gap-1.5 border-b border-gray-100 py-3 text-sm">
      <UserChip profile={item.actor} />
      <span className="text-gray-500">{i18n._(VERB_TEXT[item.verb])}</span>
      {shelfLink}
      {when}
    </li>
  )
}

// Feed is the /feed page: Following (everyone you follow, all four
// verbs - the deliberate no-algorithm discovery mechanism) and You
// (activity on your own shelves, the notifications stand-in until a
// real inbox lands). A single cursor-infinite query keyed on the tab
// covers both - switching tabs is just a fresh queryKey, no dual-query
// juggling since (unlike Explore) nothing else on this page runs
// independently of the active tab.
export default function Feed() {
  const { t, i18n } = useLingui()
  const [tab, setTab] = useState<FeedTab>('following')
  const query = useInfiniteQuery({
    queryKey: ['feed', tab],
    queryFn: ({ pageParam }: { pageParam?: string }) => fetchFeed(tab, pageParam),
    initialPageParam: undefined as string | undefined,
    getNextPageParam: (last) => last.next_cursor ?? undefined,
  })
  const items = query.data?.pages.flatMap((p) => p.items) ?? []

  return (
    <main aria-label={t`Feed`} className="py-6">
      <h2 className="mb-4 text-2xl font-bold"><Trans>Feed</Trans></h2>

      <Tabs
        label={t`Feed sections`}
        tabs={FEED_TABS.map((s): Tab<FeedTab> => ({ key: s.key, label: i18n._(s.label) }))}
        active={tab}
        onChange={setTab}
      />

      {query.isPending && <p className="mt-4 text-sm text-gray-500"><Trans>Loading feed...</Trans></p>}
      {query.isError && (
        <p role="alert" className="mt-4 text-sm text-red-700">
          <Trans>The feed cannot be loaded right now. Please try again.</Trans>
        </p>
      )}
      {!query.isPending && !query.isError && (
        items.length === 0 ? (
          tab === 'following' ? (
            <p className="py-12 text-center text-gray-500">
              <Trans>
                Nothing yet. <Link to="/explore" className="text-gray-900 hover:underline">Explore</Link> people to follow.
              </Trans>
            </p>
          ) : (
            <p className="py-12 text-center text-gray-500"><Trans>No activity yet.</Trans></p>
          )
        ) : (
          <>
            <ul className="mt-4">
              {items.map((item) => (
                <FeedRow key={item.id} item={item} />
              ))}
            </ul>
            {query.hasNextPage && (
              <button
                type="button"
                onClick={() => void query.fetchNextPage()}
                disabled={query.isFetchingNextPage}
                className="mt-4 rounded border border-gray-300 px-3 py-1 text-sm hover:bg-gray-50 disabled:opacity-50"
              >
                <Trans>Load more</Trans>
              </button>
            )}
          </>
        )
      )}
    </main>
  )
}
