import { Trans, useLingui } from '@lingui/react/macro'
import { msg, t } from '@lingui/core/macro'
import type { I18n, MessageDescriptor } from '@lingui/core'
import { useQuery } from '@tanstack/react-query'
import type { ReactNode } from 'react'
import { useState } from 'react'
import type { SearchKind } from '../../api/catalog'
import { searchCatalog } from '../../api/catalog'
import type { CatalogPick, SearchPickerState } from '../../lib/catalogPicks'
import { btnPrimary } from '../../lib/formStyles'
import SearchResultRow from './SearchResultRow'

interface SearchPickerProps {
  initialQuery?: string
  // Last-reported onStateChange snapshot; wins over initialQuery when present.
  initialState?: SearchPickerState
  onPick: (pick: CatalogPick) => void
  footer?: ReactNode
  // Which search kinds to offer; the proxy picker adds pc_listing.
  kinds?: SearchKind[]
  // Shown by default (wizard + admin adopt); price proxy picker hides it
  // since community products are priceless.
  communityLane?: 'shown' | 'hidden'
  // Fires on every kind/text/submit change with the full next state.
  onStateChange?: (s: SearchPickerState) => void
}

// pc_listing's label/noun is the PriceCharting brand name, a proper noun that
// never translates, so the tables below mix MessageDescriptor and string values.
const kindLabels: Record<SearchKind, MessageDescriptor | string> = {
  game: msg`Games`,
  hardware: msg`Hardware`,
  pc_listing: 'PriceCharting',
}
const kindPlaceholders: Record<SearchKind, MessageDescriptor> = {
  game: msg`Game title...`,
  hardware: msg`Console or accessory...`,
  pc_listing: msg`Any listing (games, variants, hardware)...`,
}
// aria-label names the kinds on offer: "games and hardware" by default
// (pinned by tests/e2e), an Oxford-comma list once PriceCharting joins.
const kindNouns: Record<SearchKind, MessageDescriptor | string> = {
  game: msg`games`,
  hardware: msg`hardware`,
  pc_listing: 'PriceCharting',
}
function resolveKindText(v: MessageDescriptor | string, i18n: I18n): string {
  return typeof v === 'string' ? v : i18n._(v)
}
// Plain function, not a component: can't call useLingui() itself, so i18n
// comes from the caller's hook, not the @lingui/core singleton (which
// wouldn't force a re-render on a later locale switch).
function searchBoxLabel(kinds: SearchKind[], i18n: I18n): string {
  const nouns = kinds.map((k) => resolveKindText(kindNouns[k], i18n))
  if (nouns.length < 2) {
    const noun = nouns.join('')
    return t(i18n)`Search for ${noun}`
  }
  if (nouns.length === 2) {
    const first = nouns[0]
    const second = nouns[1]
    return t(i18n)`Search for ${first} and ${second}`
  }
  const allButLast = nouns.slice(0, -1).join(', ')
  const last = nouns[nouns.length - 1]
  return t(i18n)`Search for ${allButLast}, and ${last}`
}

// Picking a game means picking a platform (a product is game-on-platform);
// hardware and pc_listing picks are the listing itself.
export default function SearchPicker({ initialQuery = '', initialState, onPick, footer, kinds = ['game', 'hardware'], communityLane = 'shown', onStateChange }: SearchPickerProps) {
  const { t, i18n } = useLingui()
  const [kind, setKind] = useState<SearchKind>(initialState?.kind ?? kinds[0])
  const [text, setText] = useState(initialState?.text ?? initialQuery)
  const [submitted, setSubmitted] = useState(initialState?.submitted ?? initialQuery.trim())
  const report = (next: Partial<SearchPickerState>) => onStateChange?.({ kind, text, submitted, ...next })

  const search = useQuery({
    queryKey: ['search', kind, submitted],
    queryFn: () => searchCatalog(kind, submitted),
    enabled: submitted !== '',
  })
  // Hidden-lane filter drops community rows client-side; "no results" reacts
  // to the filtered list too, or an all-community answer shows neither.
  const rows = (search.data?.results ?? []).filter(
    (r) => communityLane === 'shown' || r.origin !== 'community',
  )

  return (
    <section aria-label={t`Search`} className="flex flex-col gap-3">
      <form
        role="search"
        onSubmit={(e) => {
          e.preventDefault()
          setSubmitted(text.trim())
          report({ submitted: text.trim() })
        }}
        className="flex flex-wrap items-center gap-2"
      >
        {kinds.length > 1 && (
          <fieldset className="flex gap-2" aria-label={t`Search type`}>
            {kinds.map((k) => (
              <label key={k} className="flex items-center gap-1 text-sm">
                <input type="radio" name="kind" checked={kind === k} onChange={() => { setKind(k); report({ kind: k }) }} />
                {resolveKindText(kindLabels[k], i18n)}
              </label>
            ))}
          </fieldset>
        )}
        <input
          type="search"
          aria-label={searchBoxLabel(kinds, i18n)}
          value={text}
          onChange={(e) => { setText(e.target.value); report({ text: e.target.value }) }}
          placeholder={i18n._(kindPlaceholders[kind])}
          className="w-64 rounded border border-gray-300 px-2 py-1 text-sm"
        />
        <button
          type="submit"
          disabled={text.trim() === ''}
          className={btnPrimary}
        >
          <Trans>Search</Trans>
        </button>
      </form>

      {search.data?.degraded && (
        <p role="alert" className="rounded bg-amber-50 p-3 text-sm text-amber-800">
          <Trans>Search may be missing some results right now; try again in a moment for the full set.</Trans>
        </p>
      )}
      {search.isError && (
        <p role="alert" className="rounded bg-red-50 p-3 text-sm text-red-700">
          <Trans>Search is not working right now. Please try again.</Trans>
        </p>
      )}
      {search.isSuccess && rows.length === 0 && (
        <p className="text-sm text-gray-500"><Trans>No results for "{submitted}".</Trans></p>
      )}

      <ul className="flex flex-col gap-2">
        {rows.map((r, i) => (
          <SearchResultRow key={r.product_id ?? i} result={r} onPick={onPick} />
        ))}
      </ul>
      {footer}
    </section>
  )
}
