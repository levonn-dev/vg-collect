import { useQuery } from '@tanstack/react-query'
import { useParams } from 'react-router'
import { ApiError, fetchMe } from '../api/client'
import { fetchProfilePage } from '../api/social'
import Avatar from '../components/Avatar'
import FollowButton from '../components/social/FollowButton'
import NotFoundState from '../components/social/NotFoundState'
import ShelfCard from '../components/social/ShelfCard'
import { foldHandle } from '../lib/handle'

// Profile is the public /u/:handle page: owner card, follower/
// following counts, a follow control (hidden for the signed-in owner
// looking at their own page), and the owner's listed shelves. The
// query key folds the handle so it lines up with FollowButton's
// invalidation target regardless of the case/underscores the visitor
// typed or a link carried. An unknown handle and a private one answer
// the same 404 (deliberately indistinguishable), so the error branch
// never inspects the problem body - it renders the shared
// NotFoundState either way. social_available false means the social
// service degraded open: the page still renders, just without counts
// or a follow control (a control we cannot render correctly without
// knowing whether the viewer already follows this person).
export default function Profile() {
  const { handle = '' } = useParams()
  const me = useQuery({ queryKey: ['me'], queryFn: fetchMe })
  const profile = useQuery({
    queryKey: ['profile', foldHandle(handle)],
    queryFn: () => fetchProfilePage(handle),
  })

  if (profile.isPending) return <main className="py-8">Loading profile...</main>
  if (profile.isError) {
    if (profile.error instanceof ApiError && profile.error.status === 404) return <NotFoundState />
    return (
      <main className="py-8" role="alert">
        This profile cannot be loaded right now. Please try again.
      </main>
    )
  }

  const { profile: card, social_available, social, shelves, total_count } = profile.data
  // Defaults to "is the owner" (hides the follow control) until me
  // resolves, rather than risk a flash of Follow on the viewer's own
  // page while the identity check is still in flight.
  const isOwner = me.data ? me.data.id === card.user_id : true

  return (
    <main aria-label="Profile" className="py-6">
      <header className="flex flex-wrap items-center gap-4 border-b border-gray-200 pb-4">
        <Avatar key={card.avatar_url} url={card.avatar_url} label={card.handle} size="lg" />
        <div>
          <h2 className="text-2xl font-bold">@{card.handle}</h2>
          {social_available && social && (
            <p className="text-sm text-gray-500">
              {social.follower_count} {social.follower_count === 1 ? 'follower' : 'followers'}
              {' - '}
              {social.following_count} following
            </p>
          )}
        </div>
        {!isOwner && social_available && social && (
          <div className="ml-auto">
            <FollowButton userId={card.user_id} handle={card.handle} viewerFollows={social.viewer_follows} />
          </div>
        )}
      </header>

      <section aria-label="Shelves" className="mt-6">
        {shelves.length === 0 ? (
          <p className="py-12 text-center text-gray-500">No shared shelves yet.</p>
        ) : (
          <>
            <h3 className="mb-3 text-sm font-semibold uppercase tracking-wide text-gray-500">
              {total_count} shared {total_count === 1 ? 'shelf' : 'shelves'}
            </h3>
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
