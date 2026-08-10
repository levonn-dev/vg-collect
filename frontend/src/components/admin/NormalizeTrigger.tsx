import { Trans } from '@lingui/react/macro'
import { useMutation } from '@tanstack/react-query'
import type { NormalizeResult } from '../../api/admin'
import LeverCard from './LeverCard'

interface Props {
  title: string
  actionLabel: string
  mutationFn: () => Promise<NormalizeResult>
}

export default function NormalizeTrigger({ title, actionLabel, mutationFn }: Props) {
  const run = useMutation({ mutationFn })
  const scanned = run.data?.scanned ?? 0
  const normalized = run.data?.normalized ?? 0
  const skipped = run.data?.skipped ?? 0
  return (
    <LeverCard
      title={title}
      actionLabel={actionLabel}
      onRun={() => run.mutate()}
      pending={run.isPending}
      success={run.isSuccess
        ? <Trans>Scanned {scanned}, promoted {normalized}, skipped {skipped}.</Trans>
        : undefined}
      error={run.isError ? <Trans>The sweep failed - try again.</Trans> : undefined}
    />
  )
}
