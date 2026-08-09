import { Trans } from '@lingui/react/macro'
import { useMutation } from '@tanstack/react-query'
import type { NormalizeResult } from '../../api/admin'

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
    <section aria-label={title} className="mt-6">
      <h3 className="text-base font-semibold">{title}</h3>
      <button
        type="button"
        onClick={() => run.mutate()}
        disabled={run.isPending}
        className="mt-2 rounded border border-gray-300 px-3 py-1 text-sm hover:bg-gray-50 disabled:opacity-50"
      >
        {actionLabel}
      </button>
      {run.isSuccess && (
        <p className="mt-2 text-sm text-gray-700">
          <Trans>Scanned {scanned}, promoted {normalized}, skipped {skipped}.</Trans>
        </p>
      )}
      {run.isError && (
        <p role="alert" className="mt-2 text-sm text-red-700">
          <Trans>The sweep failed - try again.</Trans>
        </p>
      )}
    </section>
  )
}
