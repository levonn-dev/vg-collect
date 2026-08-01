import { Trans, useLingui } from '@lingui/react/macro'
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { cancelSubmission, createSubmission, fetchSubmission } from '../../api/submissions'
import type { Submission } from '../../api/submissions'
import { ApiError } from '../../api/client'

// CatalogSubmission is the custom-entry block for the shared-catalog
// pipeline: submit, watch the pending review, read a rejection,
// cancel or resubmit. Approval needs no state here - the entry turns
// product-backed and this block (custom-only) unmounts. Cancelled
// reads as never-submitted for resubmit purposes.
export default function CatalogSubmission({ entryId }: { entryId: string }) {
  const { t } = useLingui()
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
  const refresh = () => void queryClient.invalidateQueries({ queryKey: ['submission', entryId] })
  const submit = useMutation({ mutationFn: () => createSubmission(entryId), onSuccess: refresh })
  const cancel = useMutation({ mutationFn: () => cancelSubmission(entryId), onSuccess: refresh })

  if (submission.isPending) return null
  if (submission.isError)
    return (
      <p role="alert" className="mt-4 text-sm text-red-700">
        <Trans>The catalog-submission state could not be loaded.</Trans>
      </p>
    )

  const sub = submission.data
  if (sub?.status === 'pending') {
    return (
      <section aria-label={t`Catalog submission`} className="mt-4 rounded border border-gray-200 p-3 text-sm">
        <p><Trans>Submitted to the catalog - waiting for review. You can keep editing; reviewers always see your latest edits.</Trans></p>
        <button
          type="button"
          onClick={() => cancel.mutate()}
          disabled={cancel.isPending}
          className="mt-2 rounded border border-gray-300 px-3 py-1 hover:bg-gray-50 disabled:opacity-50"
        >
          <Trans>Cancel submission</Trans>
        </button>
        {cancel.isError && (
          <p role="alert" className="mt-2 text-red-700">
            {cancel.error.message}
          </p>
        )}
      </section>
    )
  }
  const rejected = sub?.status === 'rejected'
  return (
    <section aria-label={t`Catalog submission`} className="mt-4 rounded border border-gray-200 p-3 text-sm">
      {rejected && (
        <p className="mb-2">
          {sub?.reject_reason ? (
            <Trans>Submission rejected: {sub.reject_reason}</Trans>
          ) : (
            <Trans>Submission rejected.</Trans>
          )}
        </p>
      )}
      <p className="text-gray-600"><Trans>Think others own this too? Submit it to the shared catalog for review.</Trans></p>
      <button
        type="button"
        onClick={() => submit.mutate()}
        disabled={submit.isPending}
        className="mt-2 rounded border border-gray-300 px-3 py-1 hover:bg-gray-50 disabled:opacity-50"
      >
        {rejected ? <Trans>Resubmit to catalog</Trans> : <Trans>Submit to catalog</Trans>}
      </button>
      {submit.isError && (
        <p role="alert" className="mt-2 text-red-700">
          {submit.error.message}
        </p>
      )}
    </section>
  )
}
