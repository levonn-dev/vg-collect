import type { QueryClient } from '@tanstack/react-query'

// Invalidates the comment list and its derived summary counts;
// CommentComposer, CommentList, and useCommentDelete each fire it
// after their own mutation settles.
export function invalidateShelfSocial(qc: QueryClient, shelfId: string): void {
  void qc.invalidateQueries({ queryKey: ['shelfComments', shelfId] })
  void qc.invalidateQueries({ queryKey: ['shelfSummary', shelfId] })
}
