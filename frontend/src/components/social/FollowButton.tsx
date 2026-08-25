import { Trans } from '@lingui/react/macro'
import { useMutation, useQueryClient } from '@tanstack/react-query'
import { follow, unfollow } from '../../api/social'
import { btnSecondary } from '../../lib/formStyles'
import { foldHandle } from '../../lib/handle'

interface FollowButtonProps {
  userId: string
  handle: string
  viewerFollows: boolean
}

// A follow is a fact, not a resource with sibling fields to preserve, so no
// full-baseline PUT; viewerFollows is read straight off props (no local state).
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
      // Hand-rolled at btnSecondary's px-3 py-1 footprint, not btnPrimary's
      // px-4 py-2: both branches must render at the same size or the button resizes.
      className={
        viewerFollows
          ? btnSecondary
          : 'rounded bg-gray-900 px-3 py-1 text-sm text-white hover:bg-gray-700 disabled:opacity-50'
      }
    >
      {viewerFollows ? <Trans>Following</Trans> : <Trans>Follow</Trans>}
    </button>
  )
}
