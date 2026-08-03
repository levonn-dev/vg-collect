import { Trans, useLingui } from '@lingui/react/macro'
import { useInfiniteQuery, useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { fetchMe } from '../../api/client'
import { deleteComment, fetchShelfComments } from '../../api/social'
import { relativeTime } from '../../lib/relativeTime'
import { useCommentDelete } from './useCommentDelete'
import UserChip from './UserChip'

// truncateBody keeps a row's Delete/Remove accessible name distinct
// per comment - unadorned "Delete"/"Remove" reads identically for
// every row to a screen reader, with no way to tell which comment a
// given button acts on - without repeating the full body, which can
// run to the composer's 2000-char cap.
function truncateBody(body: string, max = 30): string {
  return body.length > max ? `${body.slice(0, max)}...` : body
}

// UnresolvedAuthor stands in for a comment whose author_id is present
// but the bff's hydration did not attach a card: the batched
// SharedCardsByIDs call failed open (comments still render; identity
// chips are an enhancement, not access-gated data), or the account
// behind author_id no longer resolves. A literal UUID would be
// unreadable and a guessed/truncated handle could collide with an
// unrelated real one and link to the wrong person, so this
// placeholder never fabricates an identity.
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

// DeletedUser stands in for a purge-anonymized comment: author_id
// itself comes back null, so there is no id left to hydrate or link -
// the design's anonymized rendering, distinct from UnresolvedAuthor's
// merely-unresolved placeholder.
function DeletedUser() {
  return (
    <span className="inline-flex items-center gap-1.5 text-sm text-gray-500">
      <span
        aria-hidden="true"
        className="flex h-6 w-6 items-center justify-center rounded-full bg-gray-200 text-xs font-bold text-gray-400"
      >
        ?
      </span>
      <Trans>Deleted user</Trans>
    </span>
  )
}

interface CommentListProps {
  shelfId: string
  ownerId: string
}

// CommentList renders a shelf's live comments (server order: newest
// first, keyset-paged). Two distinct delete paths sit side by side: a
// comment's own author gets the undo-window hook (requestDelete/undo
// below - the row is replaced by an inline "Comment deleted - Undo"
// toast while pending); the shelf owner removing someone else's
// comment is a moderation action with no undo, confirmed and
// committed immediately through its own mutation.
export default function CommentList({ shelfId, ownerId }: CommentListProps) {
  const { t } = useLingui()
  const me = useQuery({ queryKey: ['me'], queryFn: fetchMe })
  const { pendingIds, requestDelete, undo } = useCommentDelete(shelfId)
  const qc = useQueryClient()
  const list = useInfiniteQuery({
    queryKey: ['shelfComments', shelfId],
    queryFn: ({ pageParam }: { pageParam?: string }) => fetchShelfComments(shelfId, pageParam),
    initialPageParam: undefined as string | undefined,
    getNextPageParam: (last) => last.next_cursor,
  })
  const ownerRemove = useMutation({
    mutationFn: (id: string) => deleteComment(id),
    onSuccess: () => {
      void qc.invalidateQueries({ queryKey: ['shelfComments', shelfId] })
      void qc.invalidateQueries({ queryKey: ['shelfSummary', shelfId] })
    },
  })

  const comments = list.data?.pages.flatMap((p) => p.comments) ?? []

  return (
    <section aria-label={t`Comments`} className="mt-6">
      <h3 className="mb-3 text-sm font-semibold uppercase tracking-wide text-gray-500"><Trans>Comments</Trans></h3>
      {list.isPending && <p className="text-sm text-gray-500"><Trans>Loading comments...</Trans></p>}
      {list.isError && (
        <p role="alert" className="text-sm text-red-700">
          <Trans>Comments cannot be loaded right now. Please try again.</Trans>
        </p>
      )}
      {!list.isPending && !list.isError && (
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
                      {c.author ? (
                        <UserChip profile={c.author} />
                      ) : c.author_id == null ? (
                        <DeletedUser />
                      ) : (
                        <UnresolvedAuthor />
                      )}
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
                          onClick={() => {
                            if (
                              window.confirm(
                                t`Remove this comment? The author will not be able to restore it.`,
                              )
                            ) {
                              ownerRemove.mutate(c.id)
                            }
                          }}
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
            {list.hasNextPage && (
              <button
                type="button"
                onClick={() => void list.fetchNextPage()}
                disabled={list.isFetchingNextPage}
                className="mt-3 rounded border border-gray-300 px-3 py-1 text-sm hover:bg-gray-50 disabled:opacity-50"
              >
                <Trans>Load more</Trans>
              </button>
            )}
          </>
        )
      )}
    </section>
  )
}
