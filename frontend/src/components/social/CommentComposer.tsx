import { Trans, useLingui } from '@lingui/react/macro'
import { useMutation, useQueryClient } from '@tanstack/react-query'
import { useState } from 'react'
import { ApiError } from '../../api/client'
import { postComment } from '../../api/social'

const MAX_LENGTH = 2000

// CommentComposer posts a top-level comment onto a shared shelf. body
// keeps the raw typed text (so the counter tracks exactly what is on
// screen, including trailing whitespace); only the submitted payload
// is trimmed. A 429 (the rolling 24h comment cap) gets its own copy;
// any other failure falls back to a generic one - both read from the
// mutation's own error state, so a retried submit clears whichever
// was showing.
export default function CommentComposer({ shelfId }: { shelfId: string }) {
  const { t } = useLingui()
  const [body, setBody] = useState('')
  const qc = useQueryClient()
  const post = useMutation({
    mutationFn: () => postComment(shelfId, body.trim()),
    onSuccess: () => {
      setBody('')
      void qc.invalidateQueries({ queryKey: ['shelfComments', shelfId] })
      void qc.invalidateQueries({ queryKey: ['shelfSummary', shelfId] })
    },
  })
  const rateLimited = post.isError && post.error instanceof ApiError && post.error.status === 429
  const canPost = body.trim().length > 0

  return (
    <form
      onSubmit={(e) => {
        e.preventDefault()
        if (canPost) post.mutate()
      }}
      className="flex flex-col gap-1"
    >
      <textarea
        aria-label={t`Add a comment`}
        value={body}
        onChange={(e) => setBody(e.target.value)}
        maxLength={MAX_LENGTH}
        rows={3}
        placeholder={t`Add a comment...`}
        className="w-full rounded border border-gray-300 p-2 text-sm"
      />
      <div className="flex items-center justify-between">
        <span className="text-xs text-gray-500">{body.length}/{MAX_LENGTH}</span>
        <button
          type="submit"
          disabled={!canPost || post.isPending}
          className="rounded bg-gray-900 px-3 py-1 text-sm text-white hover:bg-gray-700 disabled:opacity-50"
        >
          <Trans>Post</Trans>
        </button>
      </div>
      {rateLimited && (
        <p role="alert" className="text-sm text-red-700">
          <Trans>Comment limit reached - try again later.</Trans>
        </p>
      )}
      {post.isError && !rateLimited && (
        <p role="alert" className="text-sm text-red-700">
          <Trans>Comment could not be posted. Please try again.</Trans>
        </p>
      )}
    </form>
  )
}
