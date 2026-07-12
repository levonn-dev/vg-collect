import type { ReactNode } from 'react'

interface ConfirmShellProps {
  ariaLabel: string
  title: string
  subtitle: ReactNode
  // The path-specific status content: a price-match card for a
  // catalog pick, a plain notice for a custom item.
  children: ReactNode
  errorMessage?: string
  onBack: () => void
  onSubmit: () => void
  submitPending: boolean
}

// ConfirmShell is the layout both wizard confirm steps share: heading,
// subtitle, the caller's own status content, an optional error banner,
// and the Back / Add to collection actions. It holds no resolve or
// submit logic of its own - callers own their mutation and pass its
// state in.
export default function ConfirmShell({
  ariaLabel, title, subtitle, children, errorMessage, onBack, onSubmit, submitPending,
}: ConfirmShellProps) {
  return (
    <section aria-label={ariaLabel} className="flex flex-col gap-3">
      <h3 className="text-lg font-semibold">Confirm: {title}</h3>
      <p className="text-sm text-gray-600">{subtitle}</p>
      {children}
      {errorMessage && (
        <p role="alert" className="rounded bg-red-50 p-3 text-sm text-red-700">
          {errorMessage}
        </p>
      )}
      <div className="flex gap-2">
        <button onClick={onBack} className="rounded border border-gray-300 px-3 py-1 text-sm hover:bg-gray-50">
          Back
        </button>
        <button
          onClick={onSubmit}
          disabled={submitPending}
          className="rounded bg-gray-900 px-4 py-1 text-sm text-white enabled:hover:bg-gray-700 disabled:opacity-50"
        >
          Add to collection
        </button>
      </div>
    </section>
  )
}
