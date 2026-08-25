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
import { componentsParametersTabValues } from '../api/schema'
import { refetchWarning, renderQueryState } from '../lib/queryBoundary'
import { relativeTime } from '../lib/relativeTime'
import { tabButtonId } from '../lib/tabs'
import { useDocumentTitle } from '../lib/useDocumentTitle'

// Tabs.tsx has no i18n of its own, so labels stay msg descriptors at
// module scope (same shape as Explore's SORT_TABS), resolved in the
// component body. Keyed by the generated schema's tab values, not a
// hand-typed list.
const tabLabels: Record<FeedTab, MessageDescriptor> = {
  following: msg`Following`,
  you: msg`You`,
}
const PANEL_IDS: Record<FeedTab, string> = {
  following: 'feed-following-panel',
  you: 'feed-you-panel',
}
const FEED_TABS: { key: FeedTab; label: MessageDescriptor; panelId: string }[] =
  componentsParametersTabValues.map((key) => ({ key, label: tabLabels[key], panelId: PANEL_IDS[key] }))

function shelfHref(shelf: ShelfCardData): string {
  return `/u/${shelf.owner.handle}/shelves/${shelf.slug}`
}

// One Trans per verb sentence, chips as placeholders, so locale
// controls word order (Japanese SOV can't fit a flex sequence). Stays
// inline text, never flex (bare words open particle gaps). align-middle
// centers chips on the baseline (else the avatar's bottom edge).
const SENTENCE_CLASS = 'text-gray-500 [&>a]:align-middle'

// States its own ink (else inherits gray-500 from the sentence span);
// gray-900 matches UserChip's handle color.
const SHELF_LINK_CLASS = 'font-medium text-gray-900 hover:underline'

// actor always attaches; shelf/followed_user/comment_excerpt ride only
// for verbs whose payload carries them. Each branch guards its field,
// so a malformed row degrades to nothing instead of crashing.
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

// Following (everyone you follow, all four verbs) and You (own-shelf
// activity, notifications stand-in). Single cursor-infinite query
// keyed on tab; switching is just a fresh queryKey.
export default function Feed() {
  const { t, i18n } = useLingui()
  useDocumentTitle(t`Feed`)
  const [tab, setTab] = useState<FeedTab>('following')
  const query = useInfiniteQuery({
    queryKey: ['feed', tab],
    queryFn: ({ pageParam }: { pageParam?: string }) => fetchFeed(tab, pageParam),
    initialPageParam: undefined as string | undefined,
    getNextPageParam: (last) => last.next_cursor ?? undefined,
  })
  const items = query.data?.pages.flatMap((p) => p.items) ?? []

  return (
    <main id="main-content" tabIndex={-1} aria-label={t`Feed`} className="py-6">
      <h2 className="mb-4 text-2xl font-bold"><Trans>Feed</Trans></h2>

      <Tabs
        label={t`Feed sections`}
        tabs={FEED_TABS.map((s): Tab<FeedTab> => ({ key: s.key, label: i18n._(s.label), panelId: s.panelId }))}
        active={tab}
        onChange={setTab}
      />

      <div role="tabpanel" id={PANEL_IDS[tab]} aria-labelledby={tabButtonId(PANEL_IDS[tab])}>
        {refetchWarning(query)}
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
      </div>
    </main>
  )
}
