import { useLingui } from '@lingui/react/macro'
import { msg } from '@lingui/core/macro'
import type { MessageDescriptor } from '@lingui/core'

export type Visibility = 'private' | 'unlisted' | 'listed'

const OPTIONS: [Visibility, MessageDescriptor, string][] = [
  ['private', msg`Private`, 'M5 8V6a3 3 0 1 1 6 0v2h1v6H4V8h1zm2-2a1 1 0 1 1 2 0v2H7V6z'],
  ['unlisted', msg`Unlisted`, 'M6 3a3 3 0 0 0-3 3v1h2V6a1 1 0 1 1 2 0v1h2V6a3 3 0 0 0-3-3zm-3 6h10v5H3V9z'],
  ['listed', msg`Listed`, 'M8 2a6 6 0 1 0 0 12A6 6 0 0 0 8 2zM3.5 8a4.5 4.5 0 0 1 9 0 4.5 4.5 0 0 1-9 0z'],
]

interface VisibilityControlProps {
  value: Visibility
  onChange: (next: Visibility) => void
  disabled?: boolean
}

// VisibilityControl is the lock/link/globe segmented control shared by
// every place a shelf or saved view picks its own visibility (Account
// keeps its own radio group for profile_visibility - a plain form
// field with a Save step, not a live-updating row control). Each
// segment is its own button, not a radio group, so a click commits
// immediately - callers wire onChange straight into a mutation.
export default function VisibilityControl({ value, onChange, disabled }: VisibilityControlProps) {
  const { i18n } = useLingui()
  return (
    <div className="flex overflow-hidden rounded border border-gray-300">
      {OPTIONS.map(([v, label, path]) => (
        <button
          key={v}
          type="button"
          aria-label={i18n._(label)}
          aria-pressed={value === v}
          disabled={disabled}
          onClick={() => onChange(v)}
          className={`p-1.5 disabled:opacity-50 ${
            value === v ? 'bg-gray-900 text-white' : 'text-gray-500 hover:bg-gray-50'
          }`}
        >
          <svg viewBox="0 0 16 16" width="16" height="16" fill="currentColor" aria-hidden="true">
            <path d={path} />
          </svg>
        </button>
      ))}
    </div>
  )
}
