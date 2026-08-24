import { Trans, useLingui } from '@lingui/react/macro'
import { msg } from '@lingui/core/macro'
import type { I18n, MessageDescriptor } from '@lingui/core'
import { useMutation, useQueryClient } from '@tanstack/react-query'
import { cancelSubmission, createSubmission } from '../../api/submissions'
import { btnSecondary } from '../../lib/formStyles'
import { resolveApiError } from '../../lib/resolveApiError'
import { useSubmission } from './useSubmission'

// entry_not_custom never actually reaches submit: this block only
// ever mounts for a custom entry (EntryDetail's !e.product_id gate),
// so the one code the endpoint documents that this call cannot
// answer is left out on purpose, not overlooked.
const submitErrorCodes: Record<string, MessageDescriptor> = {
  entry_not_found: msg`This entry no longer exists.`,
  submission_pending: msg`A submission is already pending for this entry.`,
  too_many_pending_submissions: msg`You already have too many submissions pending review.`,
  submission_rate_limited: msg`Too many submissions recently - try again later.`,
}
function submitErrorMessage(e: unknown, i18n: I18n): string {
  return resolveApiError(e, i18n, submitErrorCodes, msg`The entry could not be submitted.`)
}

const cancelErrorCodes: Record<string, MessageDescriptor> = {
  submission_not_found: msg`Nothing is pending to cancel.`,
}
function cancelErrorMessage(e: unknown, i18n: I18n): string {
  return resolveApiError(e, i18n, cancelErrorCodes, msg`The cancellation failed.`)
}

// CatalogSubmission is the custom-entry block for the shared-catalog
// pipeline: submit, watch the pending review, read a rejection,
// cancel or resubmit. Approval needs no state here - the entry turns
// product-backed and this block (custom-only) unmounts. Cancelled
// reads as never-submitted for resubmit purposes.
export default function CatalogSubmission({ entryId }: { entryId: string }) {
  const { t, i18n } = useLingui()
  const queryClient = useQueryClient()
  const submission = useSubmission(entryId)
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
          className={`${btnSecondary} mt-2`}
        >
          <Trans>Cancel submission</Trans>
        </button>
        {cancel.isError && (
          <p role="alert" className="mt-2 text-red-700">
            {cancelErrorMessage(cancel.error, i18n)}
          </p>
        )}
      </section>
    )
  }
  const rejected = sub?.status === 'rejected'
  const rejectReason = sub?.reject_reason
  return (
    <section aria-label={t`Catalog submission`} className="mt-4 rounded border border-gray-200 p-3 text-sm">
      {rejected && (
        <p className="mb-2">
          {sub?.reject_reason ? (
            <Trans>Submission rejected: {rejectReason}</Trans>
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
        className={`${btnSecondary} mt-2`}
      >
        {rejected ? <Trans>Resubmit to catalog</Trans> : <Trans>Submit to catalog</Trans>}
      </button>
      {submit.isError && (
        <p role="alert" className="mt-2 text-red-700">
          {submitErrorMessage(submit.error, i18n)}
        </p>
      )}
    </section>
  )
}
