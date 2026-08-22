import type { ReactNode } from 'react'
import { btnSecondary } from '../../lib/formStyles'

interface Props {
  title: string
  actionLabel: string
  onRun: () => void
  pending: boolean
  success?: ReactNode
  error?: ReactNode
}

// LeverCard is the shared shell for the Maintenance grid: title, one
// action button, and a status line showing the latest run's outcome.
// Callers own the mutation and the messages; the card only renders
// them, so it stays ignorant of what each lever does.
export default function LeverCard({ title, actionLabel, onRun, pending, success, error }: Props) {
  return (
    <section aria-label={title} className="rounded border border-gray-200 p-3">
      <h4 className="text-sm font-semibold">{title}</h4>
      <button
        type="button"
        onClick={onRun}
        disabled={pending}
        className={`${btnSecondary} mt-2`}
      >
        {actionLabel}
      </button>
      {success !== undefined && <p className="mt-2 text-xs text-gray-700">{success}</p>}
      {error !== undefined && (
        <p role="alert" className="mt-2 text-xs text-red-700">{error}</p>
      )}
    </section>
  )
}
