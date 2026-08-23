import { useRef, type KeyboardEvent } from 'react'

export interface Tab<T extends string> {
  key: T
  label: string
}

interface TabsProps<T extends string> {
  // Accessible name for the tablist itself (Admin's "Admin sections"/
  // Explore's own "Explore sort" idiom - every tablist needs one).
  label: string
  tabs: readonly Tab<T>[]
  active: T
  onChange: (key: T) => void
  // Positional spacing only (e.g. Explore's "mt-6" below the search
  // box); the tablist/tab visual classes themselves are fixed below.
  className?: string
}

// Tabs is the shared two-plus-tab strip: Explore's sort switch,
// Feed's Following/You switch, Collection's sections switch, and
// Admin's Mappings/Submissions switch all render through this.
// Implements the WAI-ARIA tabs
// pattern's selection-follows-focus variant with a proper roving
// tabindex: only the active tab sits in the page tab order (tabIndex
// 0), every other tab is -1, and ArrowLeft/ArrowRight/Home/End both
// move focus AND fire onChange in one step, same as a native radio
// group under arrow keys.
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
          aria-selected={active === t.key}
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
