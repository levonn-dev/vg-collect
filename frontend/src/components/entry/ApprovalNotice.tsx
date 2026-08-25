import { Trans, useLingui } from '@lingui/react/macro'
import { ackSubmissionResolution } from '../../api/submissions'
import DismissibleNotice from './DismissibleNotice'
import { useDismissibleAck } from './useDismissibleAck'
import { useSubmission } from './useSubmission'

// Closing stamps resolution_ack_at server-side, so the banner doesn't
// reappear on the next open. Custom entries never reach an approved
// submission, so this and CatalogSubmission never co-render.
export default function ApprovalNotice({ entryId }: { entryId: string }) {
  const { t } = useLingui()
  const submission = useSubmission(entryId)
  const { dismissed, dismiss } = useDismissibleAck(
    () => ackSubmissionResolution(entryId),
    ['submission', entryId],
  )

  const sub = submission.data
  if (submission.isPending || submission.isError) return null
  if (!sub || sub.status !== 'approved' || sub.resolution_ack_at || dismissed) return null
  return (
    <DismissibleNotice tone="green" dismissLabel={t`Dismiss approval notice`} onDismiss={dismiss}>
      <Trans>Your submission was approved and added to the shared catalog.</Trans>
    </DismissibleNotice>
  )
}
