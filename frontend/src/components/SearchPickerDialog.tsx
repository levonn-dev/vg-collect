import { Trans } from '@lingui/react/macro'
import type { ReactNode } from 'react'
import { useEffect, useRef } from 'react'

interface SearchPickerDialogProps {
  ariaLabel: string
  title: ReactNode
  onClose: () => void
  children: ReactNode
}

// Focus-managed dialog shell shared by ProxyPicker and ManualMatchPicker.
// Callers own everything below the header.
export default function SearchPickerDialog({ ariaLabel, title, onClose, children }: SearchPickerDialogProps) {
  const dialogRef = useRef<HTMLDivElement>(null)

  // Moves focus in on open for keyboard/screen-reader users, and restores it
  // to whatever had it on unmount.
  useEffect(() => {
    const opener = document.activeElement as HTMLElement | null
    dialogRef.current?.querySelector<HTMLElement>('input')?.focus()
    return () => opener?.focus()
  }, [])

  return (
    <div
      ref={dialogRef}
      role="dialog"
      aria-label={ariaLabel}
      onKeyDown={(e) => { if (e.key === 'Escape') onClose() }}
      className="mt-3 rounded border border-gray-300 bg-gray-50 p-3"
    >
      <div className="mb-2 flex items-center justify-between">
        <p className="text-sm font-semibold">{title}</p>
        <button type="button" onClick={onClose} className="text-sm text-gray-500 hover:text-gray-900">
          <Trans>Close</Trans>
        </button>
      </div>
      {children}
    </div>
  )
}
