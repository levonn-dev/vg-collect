import { useRef, type KeyboardEvent } from 'react'
import { tabButtonId } from '../lib/tabs'

export interface Tab<T extends string> {
  key: T
  label: string
  // tabButtonId(panelId) becomes this tab's DOM id and aria-controls; the
  // caller's panel is role="tabpanel" with a matching id and
  // aria-labelledby={tabButtonId(panelId)}. Optional: no caller needs it today.
  panelId?: string
}

interface TabsProps<T extends string> {
  // Accessible name for the tablist (e.g. Admin's "Admin sections").
  label: string
  tabs: readonly Tab<T>[]
  active: T
  onChange: (key: T) => void
  // Positional spacing only; tablist/tab visual classes are fixed below.
  className?: string
}

// WAI-ARIA tabs pattern, selection-follows-focus: only the active tab has
// tabIndex 0 (others -1), and ArrowLeft/Right/Home/End move focus and fire
// onChange in one step, like a native radio group.
export default function Tabs<T extends string>({ label, tabs, active, onChange, className = '' }: TabsProps<T>) {
  const buttonRefs = useRef<(HTMLButtonElement | null)[]>([])

  function onKeyDown(e: KeyboardEvent<HTMLButtonElement>, index: number) {
    let next: number
    if (e.key === 'ArrowRight') next = (index + 1) % tabs.length
    else if (e.key === 'ArrowLeft') next = (index - 1 + tabs.length) % tabs.length
    else if (e.key === 'Home') next = 0
    else if (e.key === 'End') next = tabs.length - 1
    else return
    e.preventDefault()
    onChange(tabs[next].key)
    buttonRefs.current[next]?.focus()
  }

  return (
    <div role="tablist" aria-label={label} className={`${className} flex gap-1 border-b border-gray-200`.trim()}>
      {tabs.map((t, i) => (
        <button
          key={t.key}
          ref={(el) => { buttonRefs.current[i] = el }}
          type="button"
          role="tab"
          id={t.panelId ? tabButtonId(t.panelId) : undefined}
          aria-selected={active === t.key}
          aria-controls={t.panelId}
          tabIndex={active === t.key ? 0 : -1}
          onClick={() => onChange(t.key)}
          onKeyDown={(e) => onKeyDown(e, i)}
          className={
            active === t.key
              ? 'border-b-2 border-gray-900 px-3 py-1 text-sm font-semibold'
              : 'px-3 py-1 text-sm text-gray-500 hover:text-gray-900'
          }
        >
          {t.label}
        </button>
      ))}
    </div>
  )
}
