import { useQuery } from '@tanstack/react-query'
import { useMemo, useState } from 'react'
import { fetchPlatforms } from '../../api/platforms'

export interface PlatformValue {
  platformIgdbId?: number
  platformName: string
}

// PlatformPicker replaces the free-text platform input across the
// wizard, the entry form, and the admin curation form. It filters the
// cached /api/platforms catalog client-side (name + aliases,
// case-insensitive) as the user types - the submit-then-list SearchPicker
// idiom, but the list is small and local so it filters live. An explicit
// escape hatch reveals a plain input for platforms the catalog does not
// list (stored name-only, no id).
//
// A confirmed pick is driven entirely by the value prop
// (value.platformIgdbId !== undefined), not local state: once a
// canonical platform is set, the input and suggestion list (which would
// otherwise keep matching the picked name against itself) are replaced
// by the name as text plus a Change button. Being value-driven means an
// entry that already carries a canonical platform opens confirmed, a
// wizard Back that retains state re-enters it, and an external reset of
// the value exits it - all for free, with no extra state to sync.
export default function PlatformPicker({ value, onChange }: { value: PlatformValue; onChange: (v: PlatformValue) => void }) {
  const platforms = useQuery({ queryKey: ['platforms'], queryFn: fetchPlatforms, staleTime: Infinity })
  const [query, setQuery] = useState(value.platformName)
  const [freeText, setFreeText] = useState(value.platformIgdbId === undefined && value.platformName !== '')

  const matches = useMemo(() => {
    const q = query.trim().toLowerCase()
    if (q === '' || freeText) return []
    const rows = platforms.data?.platforms ?? []
    return rows
      .filter(
        (p) =>
          p.name.toLowerCase().includes(q) ||
          p.aliases.some((a) => a.toLowerCase().includes(q)),
      )
      .slice(0, 8)
  }, [query, freeText, platforms.data])

  const inputClass = 'rounded border border-gray-300 px-2 py-1 text-sm'
  const labelClass = 'flex flex-col gap-1 text-sm font-medium'
  const linkButtonClass = 'self-start text-xs text-gray-500 underline'

  if (value.platformIgdbId !== undefined) {
    return (
      <div className="flex flex-col gap-1">
        <div className={labelClass}>
          Platform
          <span className="text-sm font-normal">{value.platformName}</span>
        </div>
        <button
          type="button"
          aria-label="Change platform"
          onClick={() => {
            setQuery('')
            onChange({ platformIgdbId: undefined, platformName: '' })
          }}
          className={linkButtonClass}
        >
          Change
        </button>
      </div>
    )
  }

  if (freeText) {
    return (
      <div className="flex flex-col gap-1">
        <label className={labelClass}>
          Platform
          <input
            aria-label="Platform"
            value={query}
            onChange={(e) => {
              setQuery(e.target.value)
              onChange({ platformIgdbId: undefined, platformName: e.target.value })
            }}
            className={inputClass}
          />
        </label>
        <button
          type="button"
          onClick={() => {
            setFreeText(false)
            setQuery('')
            onChange({ platformIgdbId: undefined, platformName: '' })
          }}
          className={linkButtonClass}
        >
          Pick from the catalog instead
        </button>
      </div>
    )
  }

  return (
    <div className="flex flex-col gap-1">
      <label className={labelClass}>
        Platform
        <input
          aria-label="Platform"
          value={query}
          onChange={(e) => {
            setQuery(e.target.value)
            // Typing clears a prior canonical pick until a new one lands.
            onChange({ platformIgdbId: undefined, platformName: e.target.value })
          }}
          placeholder="Start typing (SNES, PlayStation...)"
          className={inputClass}
        />
      </label>
      {matches.length > 0 && (
        <ul className="flex flex-col gap-0.5">
          {matches.map((p) => (
            <li key={p.igdb_id}>
              <button
                type="button"
                onClick={() => {
                  onChange({ platformIgdbId: p.igdb_id, platformName: p.name })
                  // The confirmed state renders the prop, not query - reset
                  // it so a later Change starts from a clean input instead
                  // of resurfacing this pick's leftover search text.
                  setQuery('')
                }}
                className="w-full rounded border border-gray-200 px-2 py-0.5 text-left text-sm hover:bg-gray-50"
              >
                {p.name}
              </button>
            </li>
          ))}
        </ul>
      )}
      <button
        type="button"
        onClick={() => {
          setFreeText(true)
          onChange({ platformIgdbId: undefined, platformName: query })
        }}
        className={linkButtonClass}
      >
        My platform isn't listed
      </button>
    </div>
  )
}
