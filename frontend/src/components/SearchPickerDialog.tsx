import { Trans } from '@lingui/react/macro'
import type { ReactNode } from 'react'
import { useEffect, useRef } from 'react'

interface SearchPickerDialogProps {
  ariaLabel: string
  title: ReactNode
  onClose: () => void
  children: ReactNode
}

// SearchPickerDialog is the modal shell ProxyPicker and ManualMatchPicker
// both wrap around a SearchPicker: a focus-managed dialog with a header
// and Close button. Callers own everything below the header - their own
// SearchPicker call plus whatever pending/error status they need
// (ProxyPicker's resolve step; ManualMatchPicker has none).
export default function SearchPickerDialog({ ariaLabel, title, onClose, children }: SearchPickerDialogProps) {
  const dialogRef = useRef<HTMLDivElement>(null)

  // A dialog opens on top of the page's own focus; move focus in so
  // keyboard and screen-reader users land inside it, and give it back
  // to whatever had it when this closes (unmounts).
  useEffect(() => {
    const opener = document.activeElement as HTMLElement | null
    dialogRef.current?.querySelector<HTMLElement>('input')?.focus()
    return () => opener?.focus()
  }, [])

  return (
    <div
      ref={dialogRef}
      role="dialog"
      aria-modal="true"
      aria-label={ariaLabel}
      className="mt-3 rounded border border-gray-300 bg-gray-50 p-3"
    >
      <div className="mb-2 flex items-center justify-between">
        <p className="text-sm font-semibold">{title}</p>
        <button onClick={onClose} className="text-sm text-gray-500 hover:text-gray-900">
          <Trans>Close</Trans>
        </button>
      </div>
      {children}
    </div>
  )
}
