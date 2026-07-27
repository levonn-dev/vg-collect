import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { useState } from 'react'
import { ApiError } from '../../api/client'
import { ackSubmissionResolution, fetchSubmission } from '../../api/submissions'
import type { Submission } from '../../api/submissions'

// ApprovalNotice is the entry-page banner for a just-approved
// submission: the entry silently turned product-backed (the
// custom-only CatalogSubmission block unmounted), so this closable
// banner tells the owner. Closing stamps resolution_ack_at server-side,
// so the banner does not reappear on the next open. Custom entries
// never reach an approved submission, so this and CatalogSubmission
// never co-render.
export default function ApprovalNotice({ entryId }: { entryId: string }) {
  const queryClient = useQueryClient()
  const submission = useQuery({
    queryKey: ['submission', entryId],
    queryFn: async (): Promise<Submission | null> => {
      try {
        return await fetchSubmission(entryId)
      } catch (e) {
        if (e instanceof ApiError && e.status === 404) return null
        throw e
      }
    },
    retry: false,
  })
  const [dismissed, setDismissed] = useState(false)
  const ack = useMutation({
    mutationFn: () => ackSubmissionResolution(entryId),
    onSuccess: () => void queryClient.invalidateQueries({ queryKey: ['submission', entryId] }),
    onError: () => setDismissed(false),
  })

  const sub = submission.data
  if (submission.isPending || submission.isError) return null
  if (!sub || sub.status !== 'approved' || sub.resolution_ack_at || dismissed) return null
  return (
    <div role="status" className="mb-4 flex items-start justify-between gap-3 rounded bg-green-50 p-3 text-sm text-green-800">
      <p>Your submission was approved and added to the shared catalog.</p>
      <button
        type="button"
        aria-label="Dismiss approval notice"
        onClick={() => {
          setDismissed(true)
          ack.mutate()
        }}
        className="shrink-0 rounded border border-green-300 px-2 py-0.5 hover:bg-white"
      >
        Dismiss
      </button>
    </div>
  )
}
