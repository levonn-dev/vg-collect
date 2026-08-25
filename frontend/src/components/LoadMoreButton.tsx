import { Trans } from '@lingui/react/macro'
import { btnSecondary } from '../lib/formStyles'

// Structural subset of useInfiniteQuery's result; no TData/TError generics needed.
interface LoadMoreQuery {
  hasNextPage: boolean
  isFetchingNextPage: boolean
  fetchNextPage: () => unknown
}

interface LoadMoreButtonProps {
  query: LoadMoreQuery
  // Only value that differs per caller: mt-4 under Tabs, mt-3 for
  // CommentList, mt-2 for the admin worklists.
  className: string
}

// Renders nothing with no next page, so callers need no hasNextPage guard.
export default function LoadMoreButton({ query, className }: LoadMoreButtonProps) {
  if (!query.hasNextPage) return null
  return (
    <button
      type="button"
      onClick={() => void query.fetchNextPage()}
      disabled={query.isFetchingNextPage}
      className={`${className} ${btnSecondary}`}
    >
      <Trans>Load more</Trans>
    </button>
  )
}
