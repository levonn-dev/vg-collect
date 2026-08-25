import { Trans, useLingui } from '@lingui/react/macro'
import { useInfiniteQuery, useMutation, useQueryClient } from '@tanstack/react-query'
import { deleteComment, fetchShelfComments } from '../../api/social'
import { confirmThen } from '../../lib/confirm'
import { refetchWarning, renderQueryState } from '../../lib/queryBoundary'
import { relativeTime } from '../../lib/relativeTime'
import { invalidateShelfSocial } from '../../lib/shelfQueries'
import { useMe } from '../../lib/useMe'
import LoadMoreButton from '../LoadMoreButton'
import SectionLabel from '../SectionLabel'
import { useCommentDelete } from './useCommentDelete'
import UserChip from './UserChip'

// Keeps each row's Delete/Remove accessible name distinct: unadorned "Delete"
// reads identically for every row to a screen reader, but the full body can
// run to the composer's 2000-char cap.
function truncateBody(body: string, max = 30): string {
  return body.length > max ? `${body.slice(0, max)}...` : body
}

// Author card missing though author_id is present: SharedCardsByIDs failed
// open (chips are an enhancement, not access-gated) or the account no longer
// resolves. Never fabricates an identity: a truncated handle could collide
// with an unrelated real one and link to the wrong person.
function UnresolvedAuthor() {
  return (
    <span className="inline-flex items-center gap-1.5 text-sm text-gray-500">
      <span
        aria-hidden="true"
        className="flex h-6 w-6 items-center justify-center rounded-full bg-gray-200 text-xs font-bold text-gray-400"
      >
        ?
      </span>
      <Trans>Member</Trans>
    </span>
  )
}

interface CommentListProps {
  shelfId: string
  ownerId: string
}

// Own author gets the undo-window hook (requestDelete/undo): row shows an
// inline "Comment deleted - Undo" toast while pending. Owner removing someone
// else's is moderation: no undo, confirmed and committed immediately.
export default function CommentList({ shelfId, ownerId }: CommentListProps) {
  const { t } = useLingui()
  const me = useMe()
  const { pendingIds, requestDelete, undo } = useCommentDelete(shelfId)
  const qc = useQueryClient()
  const list = useInfiniteQuery({
    queryKey: ['shelfComments', shelfId],
    queryFn: ({ pageParam }: { pageParam?: string }) => fetchShelfComments(shelfId, pageParam),
    initialPageParam: undefined as string | undefined,
    getNextPageParam: (last) => last.next_cursor,
  })
  // onSettled, not onSuccess: a failed removal must still invalidate so the
  // row resyncs with server truth.
  const ownerRemove = useMutation({
    mutationFn: (id: string) => deleteComment(id),
    onSettled: () => invalidateShelfSocial(qc, shelfId),
  })

  const comments = list.data?.pages.flatMap((p) => p.comments) ?? []

  return (
    <section aria-label={t`Comments`} className="mt-6">
      <SectionLabel as="h3" size="sm" className="mb-3"><Trans>Comments</Trans></SectionLabel>
      {ownerRemove.isError && (
        <p role="alert" className="mb-2 text-sm text-red-700">
          <Trans>Comment could not be removed. Please try again.</Trans>
        </p>
      )}
      {refetchWarning(list)}
      {renderQueryState(list, {
        size: 'subsection',
        role: 'alert',
        loading: <Trans>Loading comments...</Trans>,
        error: <Trans>Comments cannot be loaded right now. Please try again.</Trans>,
      }) ?? (
        comments.length === 0 ? (
          <p className="text-sm text-gray-500"><Trans>No comments yet.</Trans></p>
        ) : (
          <>
            <ul className="flex flex-col gap-3">
              {comments.map((c) => {
                if (pendingIds.has(c.id)) {
                  return (
                    <li key={c.id} role="status" className="text-sm text-gray-500">
                      <Trans>
                        Comment deleted -{' '}
                        <button
                          type="button"
                          onClick={() => undo(c.id)}
                          className="text-amber-600 hover:underline"
                        >
                          Undo
                        </button>
                      </Trans>
                    </li>
                  )
                }
                const isSelf = me.data?.id === c.author_id
                const canModerate = !isSelf && me.data?.id === ownerId
                const bodyPreview = truncateBody(c.body)
                return (
                  <li key={c.id} className="flex flex-col gap-1">
                    <div className="flex flex-wrap items-center gap-2">
                      {c.author ? <UserChip profile={c.author} /> : <UnresolvedAuthor />}
                      <span className="text-xs text-gray-400">{relativeTime(c.created_at)}</span>
                      {isSelf && (
                        <button
                          type="button"
                          onClick={() => requestDelete(c.id)}
                          aria-label={t`Delete your comment: ${bodyPreview}`}
                          className="ml-auto text-xs text-gray-500 hover:text-red-700"
                        >
                          <Trans>Delete</Trans>
                        </button>
                      )}
                      {canModerate && (
                        <button
                          type="button"
                          onClick={() =>
                            confirmThen(
                              t`Remove this comment? The author will not be able to restore it.`,
                              () => ownerRemove.mutate(c.id),
                            )
                          }
                          disabled={ownerRemove.isPending}
                          aria-label={t`Remove comment: ${bodyPreview}`}
                          className="ml-auto text-xs text-gray-500 hover:text-red-700 disabled:opacity-50"
                        >
                          <Trans>Remove</Trans>
                        </button>
                      )}
                    </div>
                    <p className="text-sm">{c.body}</p>
                  </li>
                )
              })}
            </ul>
            <LoadMoreButton query={list} className="mt-3" />
          </>
        )
      )}
    </section>
  )
}
