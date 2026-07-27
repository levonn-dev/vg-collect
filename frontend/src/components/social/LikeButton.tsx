import { useMutation, useQueryClient } from '@tanstack/react-query'
import { like, unlike } from '../../api/social'

interface LikeButtonProps {
  shelfId: string
  viewerLikes: boolean
  count: number
}

// LikeButton mirrors FollowButton: a dedicated PUT/DELETE edge, no
// local optimistic count. count is read straight off the caller's
// props and only moves once the invalidated summary re-fetches - a
// deliberate optimistic-free tradeoff so a failed request never
// leaves a stale, too-high count on screen.
export default function LikeButton({ shelfId, viewerLikes, count }: LikeButtonProps) {
  const queryClient = useQueryClient()
  const toggle = useMutation({
    mutationFn: () => (viewerLikes ? unlike(shelfId) : like(shelfId)),
    onSuccess: () => {
      void queryClient.invalidateQueries({ queryKey: ['shelfSummary', shelfId] })
      void queryClient.invalidateQueries({ queryKey: ['sharedShelf'] })
    },
  })
  return (
    <button
      type="button"
      onClick={() => toggle.mutate()}
      disabled={toggle.isPending}
      aria-pressed={viewerLikes}
      aria-label={viewerLikes ? 'Unlike' : 'Like'}
      className={`flex items-center gap-1 text-sm ${
        viewerLikes ? 'text-red-700' : 'text-gray-300 hover:text-red-700'
      }`}
    >
      <span aria-hidden="true">{'\u2665'}</span>
      <span>{count}</span>
    </button>
  )
}
