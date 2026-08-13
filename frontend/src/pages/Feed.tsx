import { Trans, useLingui } from '@lingui/react/macro'
import { msg } from '@lingui/core/macro'
import type { MessageDescriptor } from '@lingui/core'
import { useInfiniteQuery } from '@tanstack/react-query'
import { useState } from 'react'
import { Link } from 'react-router'
import type { FeedItem, FeedTab, ShelfCard as ShelfCardData } from '../api/social'
import { fetchFeed } from '../api/social'
import EmptyState from '../components/EmptyState'
import LoadMoreButton from '../components/LoadMoreButton'
import Tabs, { type Tab } from '../components/Tabs'
import UserChip from '../components/social/UserChip'
import { renderQueryState } from '../lib/queryBoundary'
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

function shelfHref(shelf: ShelfCardData): string {
  return `/u/${shelf.owner.handle}/shelves/${shelf.slug}`
}

// Each feed row is one translated sentence - a single Trans per verb
// with the chips and the shelf link inside it as component
// placeholders - so the locale controls the whole word order:
// Japanese needs SOV with its particles flush against the names,
// which a fixed actor/verb/target sequence of flex items cannot
// express. The sentence span must flow as inline text, never as a
// flex container: bare words inside a flex box become anonymous flex
// items and gap spacing opens around the particles. align-middle
// keeps the inline-flex chips optically centered on the text line;
// their natural baseline is the avatar image's bottom edge, which
// would hang the sentence text below the handles.
const SENTENCE_CLASS = 'text-gray-500 [&>a]:align-middle'

// The shelf link states its ink explicitly because it sits inside the
// gray-500 sentence span and would otherwise inherit it; gray-900
// matches UserChip's own handle color, so names and shelf titles read
// as one tier against the connective words.
const SHELF_LINK_CLASS = 'font-medium text-gray-900 hover:underline'

// FeedRow renders one activity row by verb. actor always attaches;
// shelf/followed_user/comment_excerpt ride along only for the verbs
// whose payload carries them (see FeedItem) - the bff's fill loop
// drops a row entirely when its object fails the tab's gating rule
// rather than send a shelf verb with no shelf, but each branch still
// guards its field before use so a malformed row degrades to nothing
// instead of crashing the list. The excerpt clamps to two lines via
// line-clamp-2 (same idiom as CoverGrid's card titles).
function FeedRow({ item }: { item: FeedItem }) {
  const when = <span className="ml-auto shrink-0 text-xs text-gray-400">{relativeTime(item.created_at)}</span>

  if (item.verb === 'followed_user') {
    if (!item.followed_user) return null
    return (
      <li className="flex items-center gap-1.5 border-b border-gray-100 py-3 text-sm">
        <span className={SENTENCE_CLASS}>
          <Trans>
            <UserChip profile={item.actor} /> followed <UserChip profile={item.followed_user} />
          </Trans>
        </span>
        {when}
      </li>
    )
  }

  if (!item.shelf) return null
  const shelfName = item.shelf.name
  const href = shelfHref(item.shelf)

  if (item.verb === 'commented_shelf') {
    return (
      <li className="flex flex-col gap-1 border-b border-gray-100 py-3 text-sm">
        <div className="flex items-center gap-1.5">
          <span className={SENTENCE_CLASS}>
            <Trans>
              <UserChip profile={item.actor} /> commented on <Link to={href} className={SHELF_LINK_CLASS}>{shelfName}</Link>
            </Trans>
          </span>
          {when}
        </div>
        {item.comment_excerpt && (
          <p className="line-clamp-2 text-gray-600">{item.comment_excerpt}</p>
        )}
      </li>
    )
  }

  return (
    <li className="flex items-center gap-1.5 border-b border-gray-100 py-3 text-sm">
      <span className={SENTENCE_CLASS}>
        {item.verb === 'liked_shelf' ? (
          <Trans>
            <UserChip profile={item.actor} /> liked <Link to={href} className={SHELF_LINK_CLASS}>{shelfName}</Link>
          </Trans>
        ) : (
          <Trans>
            <UserChip profile={item.actor} /> published <Link to={href} className={SHELF_LINK_CLASS}>{shelfName}</Link>
          </Trans>
        )}
      </span>
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

      {renderQueryState(query, {
        size: 'subsection',
        className: 'mt-4',
        role: 'alert',
        loading: <Trans>Loading feed...</Trans>,
        error: <Trans>The feed cannot be loaded right now. Please try again.</Trans>,
      }) ?? (
        items.length === 0 ? (
          tab === 'following' ? (
            <EmptyState size="default">
              <Trans>
                Nothing yet. <Link to="/explore" className="text-gray-900 hover:underline">Explore</Link> people to follow.
              </Trans>
            </EmptyState>
          ) : (
            <EmptyState size="default"><Trans>No activity yet.</Trans></EmptyState>
          )
        ) : (
          <>
            <ul className="mt-4">
              {items.map((item) => (
                <FeedRow key={item.id} item={item} />
              ))}
            </ul>
            <LoadMoreButton query={query} className="mt-4" />
          </>
        )
      )}
    </main>
  )
}
