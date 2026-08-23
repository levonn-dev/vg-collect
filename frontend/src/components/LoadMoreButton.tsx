import { Trans } from '@lingui/react/macro'
import { btnSecondary } from '../lib/formStyles'

// The shape every useInfiniteQuery result satisfies structurally - no
// TData/TError generics needed since this button never touches the
// page data itself, only the pager controls.
interface LoadMoreQuery {
  hasNextPage: boolean
  isFetchingNextPage: boolean
  fetchNextPage: () => unknown
}

interface LoadMoreButtonProps {
  query: LoadMoreQuery
  // The one thing that differs per site (page-level lists sit under a
  // Tabs control and use mt-4; CommentList's mt-3 and the four admin
  // worklists' mt-2 match the spacing of what is directly above them)
  // - everything else about the button is identical everywhere.
  className: string
}

// LoadMoreButton renders nothing once the query has no next page, so
// callers need no `{query.hasNextPage && ...}` guard around it.
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
