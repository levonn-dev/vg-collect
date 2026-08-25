import { Trans } from '@lingui/react/macro'
import type { ReactNode } from 'react'
import { btnPrimary, btnSecondary } from '../../lib/formStyles'

interface ConfirmShellProps {
  ariaLabel: string
  title: string
  subtitle: ReactNode
  // Path-specific status content: a price-match card for a catalog pick, a
  // plain notice for a custom item.
  children: ReactNode
  errorMessage?: string
  onBack: () => void
  onSubmit: () => void
  submitPending: boolean
}

// Holds no resolve/submit logic of its own; callers own their mutation and
// pass its state in.
export default function ConfirmShell({
  ariaLabel, title, subtitle, children, errorMessage, onBack, onSubmit, submitPending,
}: ConfirmShellProps) {
  return (
    <section aria-label={ariaLabel} className="flex flex-col gap-3">
      <h3 className="text-lg font-semibold"><Trans>Confirm: {title}</Trans></h3>
      <p className="text-sm text-gray-600">{subtitle}</p>
      {children}
      {errorMessage && (
        <p role="alert" className="rounded bg-red-50 p-3 text-sm text-red-700">
          {errorMessage}
        </p>
      )}
      <div className="flex gap-2">
        <button type="button" onClick={onBack} className={btnSecondary}>
          <Trans>Back</Trans>
        </button>
        <button
          type="button"
          onClick={onSubmit}
          disabled={submitPending}
          className={btnPrimary}
        >
          <Trans>Add to collection</Trans>
        </button>
      </div>
    </section>
  )
}
