import type { QueryClient } from '@tanstack/react-query'

// invalidateShelfSocial centralizes the invalidation pair every
// comment-mutating action on a shared shelf needs: the comment list
// itself and the summary counts derived from it. CommentComposer's
// post, CommentList's owner-remove, and useCommentDelete's commit
// each fire it after their own mutation settles.
export function invalidateShelfSocial(qc: QueryClient, shelfId: string): void {
  void qc.invalidateQueries({ queryKey: ['shelfComments', shelfId] })
  void qc.invalidateQueries({ queryKey: ['shelfSummary', shelfId] })
}
