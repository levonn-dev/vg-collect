import { Trans, useLingui } from '@lingui/react/macro'
import { useQuery } from '@tanstack/react-query'
import { useMemo, useState } from 'react'
import { fetchPlatforms } from '../../api/platforms'
import { inputClass, labelClass, linkButtonClass } from '../../lib/formStyles'

export interface PlatformValue {
  platformIgdbId?: number
  platformName: string
}

// Filters cached /api/platforms client-side (name+aliases, case-insensitive)
// live, unlike SearchPicker's submit-then-list; escape hatch stores a
// name-only platform with no id.
// Confirmed state follows value.platformIgdbId !== undefined, not local
// state, so an external reset (or wizard Back) needs no sync to exit it.
// maxLength passes through to both free-text inputs; optional so other
// callers are unchanged.
export default function PlatformPicker({ value, onChange, maxLength }: { value: PlatformValue; onChange: (v: PlatformValue) => void; maxLength?: number }) {
  const { t } = useLingui()
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

  if (value.platformIgdbId !== undefined) {
    return (
      <div className="flex flex-col gap-1">
        <div className={labelClass}>
          <Trans>Platform</Trans>
          <span className="text-sm font-normal">{value.platformName}</span>
        </div>
        <button
          type="button"
          aria-label={t`Change platform`}
          onClick={() => {
            setQuery('')
            onChange({ platformIgdbId: undefined, platformName: '' })
          }}
          className={linkButtonClass}
        >
          <Trans>Change</Trans>
        </button>
      </div>
    )
  }

  if (freeText) {
    return (
      <div className="flex flex-col gap-1">
        <label className={labelClass}>
          <Trans>Platform</Trans>
          <input
            aria-label={t`Platform`}
            value={query}
            maxLength={maxLength}
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
          <Trans>Pick from the catalog instead</Trans>
        </button>
      </div>
    )
  }

  return (
    <div className="flex flex-col gap-1">
      <label className={labelClass}>
        <Trans>Platform</Trans>
        <input
          aria-label={t`Platform`}
          value={query}
          maxLength={maxLength}
          onChange={(e) => {
            setQuery(e.target.value)
            // Typing clears a prior canonical pick until a new one lands.
            onChange({ platformIgdbId: undefined, platformName: e.target.value })
          }}
          placeholder={t`Start typing (SNES, PlayStation...)`}
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
                  // Confirmed state renders the prop, not query; reset so a
                  // later Change starts clean, not from leftover search text.
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
        <Trans>My platform isn't listed</Trans>
      </button>
    </div>
  )
}
