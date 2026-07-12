import { useQuery } from '@tanstack/react-query'
import type { ReactNode } from 'react'
import { useState } from 'react'
import type { SearchKind } from '../../api/catalog'
import { searchCatalog } from '../../api/catalog'
import { releaseYear } from '../../lib/format'
import { useDisplayMoney } from '../../lib/useDisplayMoney'
import ItemTypeIcon from '../ItemTypeIcon'

export interface GamePick {
  kind: 'game'
  igdbGameId: number
  name: string
  platformId: number
  platformName: string
}

export interface HardwarePick {
  kind: 'hardware'
  pcProductId: number
  name: string
  category: string
}

export interface PCListingPick {
  kind: 'pc_listing'
  pcProductId: number
  name: string
}

export type CatalogPick = GamePick | HardwarePick | PCListingPick

interface SearchPickerProps {
  initialQuery?: string
  onPick: (pick: CatalogPick) => void
  footer?: ReactNode
  // Which search kinds to offer; the add wizard keeps the default,
  // the proxy picker adds the all-of-PriceCharting kind.
  kinds?: SearchKind[]
}

const kindLabels: Record<SearchKind, string> = {
  game: 'Games',
  hardware: 'Hardware',
  pc_listing: 'PriceCharting',
}
const kindPlaceholders: Record<SearchKind, string> = {
  game: 'Game title...',
  hardware: 'Console or accessory...',
  pc_listing: 'Any listing (games, variants, hardware)...',
}
// The search box's aria-label names the kinds on offer: "games and
// hardware" (default two kinds, pinned by the add wizard's tests and
// e2e steps) or an Oxford-comma list once PriceCharting joins in.
const kindNouns: Record<SearchKind, string> = {
  game: 'games',
  hardware: 'hardware',
  pc_listing: 'PriceCharting',
}
function searchBoxLabel(kinds: SearchKind[]): string {
  const nouns = kinds.map((k) => kindNouns[k])
  if (nouns.length < 2) return `Search for ${nouns.join('')}`
  if (nouns.length === 2) return `Search for ${nouns[0]} and ${nouns[1]}`
  return `Search for ${nouns.slice(0, -1).join(', ')}, and ${nouns[nouns.length - 1]}`
}

// SearchPicker is the shared catalog-search surface: the add wizard's
// first step and the pricing proxy picker. Picking a game means
// picking a platform (a product is game-on-platform); hardware and
// pc_listing picks are the listing itself.
export default function SearchPicker({ initialQuery = '', onPick, footer, kinds = ['game', 'hardware'] }: SearchPickerProps) {
  const money = useDisplayMoney()
  const [kind, setKind] = useState<SearchKind>(kinds[0])
  const [text, setText] = useState(initialQuery)
  const [submitted, setSubmitted] = useState(initialQuery.trim())

  const search = useQuery({
    queryKey: ['search', kind, submitted],
    queryFn: () => searchCatalog(kind, submitted),
    enabled: submitted !== '',
  })

  return (
    <section aria-label="Search" className="flex flex-col gap-3">
      <form
        role="search"
        onSubmit={(e) => {
          e.preventDefault()
          setSubmitted(text.trim())
        }}
        className="flex flex-wrap items-center gap-2"
      >
        {kinds.length > 1 && (
          <fieldset className="flex gap-2" aria-label="Search type">
            {kinds.map((k) => (
              <label key={k} className="flex items-center gap-1 text-sm">
                <input type="radio" name="kind" checked={kind === k} onChange={() => setKind(k)} />
                {kindLabels[k]}
              </label>
            ))}
          </fieldset>
        )}
        <input
          type="search"
          aria-label={searchBoxLabel(kinds)}
          value={text}
          onChange={(e) => setText(e.target.value)}
          placeholder={kindPlaceholders[kind]}
          className="w-64 rounded border border-gray-300 px-2 py-1 text-sm"
        />
        <button
          type="submit"
          disabled={text.trim() === ''}
          className="rounded bg-gray-900 px-3 py-1 text-sm text-white enabled:hover:bg-gray-700 disabled:opacity-50"
        >
          Search
        </button>
      </form>

      {search.data?.degraded && (
        <p role="alert" className="rounded bg-amber-50 p-3 text-sm text-amber-800">
          Search may be missing some results right now; try again in a moment for the full set.
        </p>
      )}
      {search.isError && (
        <p role="alert" className="rounded bg-red-50 p-3 text-sm text-red-700">
          Search is not working right now. Please try again.
        </p>
      )}
      {search.isSuccess && search.data.results.length === 0 && (
        <p className="text-sm text-gray-500">No results for "{submitted}".</p>
      )}

      <ul className="flex flex-col gap-2">
        {search.data?.results.map((r, i) => (
          <li key={i} className="flex items-start gap-3 rounded border border-gray-200 p-2">
            {r.cover_url ? (
              <img src={r.cover_url} alt="" className="h-16 w-auto rounded" />
            ) : (
              <div aria-hidden="true" className="flex h-16 w-12 shrink-0 items-center justify-center rounded bg-gray-100 text-gray-400">
                <ItemTypeIcon
                  type={
                    r.type === 'game'
                      ? 'game'
                      : r.type === 'pc_listing'
                        ? r.category === 'Systems'
                          ? 'console'
                          : r.category === 'Controllers' || r.category === 'Accessories'
                            ? 'accessory'
                            : 'game' // no category, or a genre string: a game listing
                        : r.category === 'Systems'
                          ? 'console'
                          : 'accessory'
                  }
                  className="h-7 w-7"
                />
              </div>
            )}
            <div>
              <p className="text-sm font-medium">
                {r.name}
                {releaseYear(r.first_release_date) && (
                  <span className="ml-2 text-xs text-gray-400">{releaseYear(r.first_release_date)}</span>
                )}
                {r.console_name && <span className="ml-2 text-xs text-gray-400">{r.console_name}</span>}
                {r.category && <span className="ml-2 text-xs text-gray-400">{r.category}</span>}
              </p>
              {r.type === 'game' && r.igdb_game_id !== undefined ? (
                <p className="mt-1 flex flex-wrap items-center gap-1">
                  {/* The chips are the pick targets, not the row; say so. */}
                  <span className="text-xs text-gray-500">Add on:</span>
                  {r.platforms?.map((p) => (
                    <button
                      key={p.igdb_platform_id}
                      type="button"
                      onClick={() =>
                        onPick({
                          kind: 'game',
                          igdbGameId: r.igdb_game_id!,
                          name: r.name,
                          platformId: p.igdb_platform_id,
                          platformName: p.name,
                        })
                      }
                      aria-label={`${r.name} on ${p.name}`}
                      className="rounded border border-gray-300 px-2 py-0.5 text-xs hover:border-gray-400 hover:bg-gray-50"
                    >
                      {p.name}
                    </button>
                  ))}
                </p>
              ) : r.type === 'pc_listing' && r.pc_product_id !== undefined ? (
                <div className="mt-1 flex flex-col gap-1">
                  <p className="text-xs text-gray-500">
                    Loose {money.format(r.loose_cents) ?? '-'} / CIB {money.format(r.cib_cents) ?? '-'} / New{' '}
                    {money.format(r.new_cents) ?? '-'}
                  </p>
                  <button
                    type="button"
                    onClick={() =>
                      onPick({ kind: 'pc_listing', pcProductId: r.pc_product_id!, name: r.name })
                    }
                    className="self-start rounded border border-gray-300 px-2 py-0.5 text-xs hover:border-gray-400 hover:bg-gray-50"
                  >
                    Use {r.name}
                  </button>
                </div>
              ) : r.pc_product_id !== undefined ? (
                <button
                  type="button"
                  onClick={() =>
                    onPick({
                      kind: 'hardware',
                      pcProductId: r.pc_product_id!,
                      name: r.name,
                      category: r.category ?? '',
                    })
                  }
                  className="mt-1 rounded border border-gray-300 px-2 py-0.5 text-xs hover:border-gray-400 hover:bg-gray-50"
                >
                  Add {r.name}
                </button>
              ) : null}
            </div>
          </li>
        ))}
      </ul>
      {footer}
    </section>
  )
}
