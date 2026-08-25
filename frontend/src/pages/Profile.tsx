import { Plural, Trans, useLingui } from '@lingui/react/macro'
import { useQuery } from '@tanstack/react-query'
import { useParams } from 'react-router'
import { ApiError } from '../api/client'
import { fetchProfilePage } from '../api/social'
import Avatar from '../components/Avatar'
import EmptyState from '../components/EmptyState'
import SectionLabel from '../components/SectionLabel'
import FollowButton from '../components/social/FollowButton'
import NotFoundState from '../components/social/NotFoundState'
import ShelfCard from '../components/social/ShelfCard'
import { foldHandle } from '../lib/handle'
import { refetchWarning, renderQueryState } from '../lib/queryBoundary'
import { useDocumentTitle } from '../lib/useDocumentTitle'
import { useMe } from '../lib/useMe'

// Public /u/:handle page. Query key folds the handle to match
// FollowButton's invalidation target. Unknown and private handles both
// 404 indistinguishably. social_available false means the social
// service degraded open: renders without counts or follow control.
export default function Profile() {
  const { t } = useLingui()
  const { handle = '' } = useParams()
  const me = useMe()
  const profile = useQuery({
    queryKey: ['profile', foldHandle(handle)],
    queryFn: () => fetchProfilePage(handle),
  })
  useDocumentTitle(profile.data ? `@${profile.data.profile.handle}` : t`Profile`)

  if (profile.isPending || (profile.isError && profile.data === undefined)) {
    return renderQueryState(profile, {
      size: 'page',
      role: 'alert',
      loading: <Trans>Loading profile...</Trans>,
      error: <Trans>This profile cannot be loaded right now. Please try again.</Trans>,
      notFound: profile.isError && profile.error instanceof ApiError && profile.error.status === 404
        ? <NotFoundState />
        : undefined,
    })
  }

  const { profile: card, social_available, social, shelves, total_count } = profile.data
  // Defaults to owner (hides follow control) until me resolves,
  // avoiding a flash of Follow on your own page.
  const isOwner = me.data ? me.data.id === card.user_id : true
  const followerCount = social_available && social ? social.follower_count : 0
  const followingCount = social_available && social ? social.following_count : 0

  return (
    <main id="main-content" tabIndex={-1} aria-label={t`Profile`} className="py-6">
      {refetchWarning(profile)}
      <header className="flex flex-wrap items-center gap-4 border-b border-gray-200 pb-4">
        <Avatar key={card.avatar_url} url={card.avatar_url} label={card.handle} size="lg" />
        <div>
          <h2 className="text-2xl font-bold">@{card.handle}</h2>
          {social_available && social && (
            <p className="text-sm text-gray-500">
              <Plural value={followerCount} one="# follower" other="# followers" />
              {' - '}
              <Plural value={followingCount} one="# following" other="# following" />
            </p>
          )}
        </div>
        {!isOwner && social_available && social && (
          <div className="ml-auto">
            <FollowButton userId={card.user_id} handle={card.handle} viewerFollows={social.viewer_follows} />
          </div>
        )}
      </header>

      <section aria-label={t`Shelves`} className="mt-6">
        {shelves.length === 0 ? (
          <EmptyState size="default"><Trans>No shared shelves yet.</Trans></EmptyState>
        ) : (
          <>
            <SectionLabel as="h3" size="sm" className="mb-3">
              <Plural value={total_count} one="# shared shelf" other="# shared shelves" />
            </SectionLabel>
            <ul className="grid grid-cols-1 gap-4 sm:grid-cols-2 lg:grid-cols-3">
              {shelves.map((s) => (
                <li key={s.id}>
                  <ShelfCard card={s} />
                </li>
              ))}
            </ul>
          </>
        )}
      </section>
    </main>
  )
}
