import { useLingui } from '@lingui/react/macro'
import { visibilityLabels } from '../../lib/enumLabels'
import { visibilityValues } from '../../api/schema'

const VISIBILITY_VALUES = visibilityValues
export type Visibility = (typeof VISIBILITY_VALUES)[number]

// ICON_PATHS keyed by the generated enum, whose order already matches
// lock/link/globe, so deriving OPTIONS from it is a source-of-truth swap.
const ICON_PATHS: Record<Visibility, string> = {
  private: 'M5 8V6a3 3 0 1 1 6 0v2h1v6H4V8h1zm2-2a1 1 0 1 1 2 0v2H7V6z',
  unlisted: 'M6 3a3 3 0 0 0-3 3v1h2V6a1 1 0 1 1 2 0v1h2V6a3 3 0 0 0-3-3zm-3 6h10v5H3V9z',
  listed: 'M8 2a6 6 0 1 0 0 12A6 6 0 0 0 8 2zM3.5 8a4.5 4.5 0 0 1 9 0 4.5 4.5 0 0 1-9 0z',
}
const OPTIONS: [Visibility, string][] = VISIBILITY_VALUES.map((v): [Visibility, string] => [v, ICON_PATHS[v]])

interface VisibilityControlProps {
  value: Visibility
  onChange: (next: Visibility) => void
  disabled?: boolean
}

// Account keeps its own radio group for profile_visibility (a plain field
// with a Save step, not a live row control). Each segment is its own button,
// not a radio group, so a click commits immediately into onChange.
export default function VisibilityControl({ value, onChange, disabled }: VisibilityControlProps) {
  const { i18n } = useLingui()
  return (
    <div className="flex overflow-hidden rounded border border-gray-300">
      {OPTIONS.map(([v, path]) => (
        <button
          key={v}
          type="button"
          aria-label={i18n._(visibilityLabels[v])}
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
