import { Trans, useLingui } from '@lingui/react/macro'
import { useMutation } from '@tanstack/react-query'
import { runResnapshot } from '../../api/admin'
import LeverCard from './LeverCard'

// ResnapshotTrigger fires the same sweep the operator lever runs:
// every game-backed entry's release date, localized presentation
// trio, and credit arrays recomputed from its product's current
// data. Synchronous - the counts come back in the response.
export default function ResnapshotTrigger() {
  const { t } = useLingui()
  const run = useMutation({ mutationFn: runResnapshot })
  const seen = run.data?.products_seen ?? 0
  const failed = run.data?.products_failed ?? 0
  const updated = run.data?.entries_updated ?? 0
  return (
    <LeverCard
      title={t`Entry resnapshot`}
      actionLabel={t`Run entry resnapshot`}
      onRun={() => run.mutate()}
      pending={run.isPending}
      success={run.isSuccess
        ? <Trans>Products seen {seen}, failed {failed}, entries updated {updated}.</Trans>
        : undefined}
      error={run.isError ? <Trans>The sweep failed - try again.</Trans> : undefined}
    />
  )
}
