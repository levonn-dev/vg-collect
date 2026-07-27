import { useMutation, useQueryClient } from '@tanstack/react-query'
import { follow, unfollow } from '../../api/social'
import { foldHandle } from '../../lib/handle'

interface FollowButtonProps {
  userId: string
  handle: string
  viewerFollows: boolean
}

// FollowButton toggles the follow edge through the dedicated PUT/DELETE
// routes - a follow is a fact, not a resource with sibling fields to
// preserve, so there is no PinStar-style full-baseline replacement
// here. viewerFollows is read straight off the caller's props: the
// button owns no local state, so its next render always reflects
// whatever the invalidated profile query re-fetches.
export default function FollowButton({ userId, handle, viewerFollows }: FollowButtonProps) {
  const queryClient = useQueryClient()
  const toggle = useMutation({
    mutationFn: () => (viewerFollows ? unfollow(userId) : follow(userId)),
    onSuccess: () =>
      void queryClient.invalidateQueries({ queryKey: ['profile', foldHandle(handle)] }),
  })
  return (
    <button
      type="button"
      onClick={() => toggle.mutate()}
      disabled={toggle.isPending}
      aria-pressed={viewerFollows}
      className={
        viewerFollows
          ? 'rounded border border-gray-300 px-3 py-1 text-sm hover:bg-gray-50 disabled:opacity-50'
          : 'rounded bg-gray-900 px-3 py-1 text-sm text-white hover:bg-gray-700 disabled:opacity-50'
      }
    >
      {viewerFollows ? 'Following' : 'Follow'}
    </button>
  )
}
