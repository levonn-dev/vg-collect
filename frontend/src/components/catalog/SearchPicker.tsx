import { Trans, useLingui } from '@lingui/react/macro'
import { msg, t } from '@lingui/core/macro'
import type { I18n, MessageDescriptor } from '@lingui/core'
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

export interface CommunityPick {
  kind: 'community'
  productId: string
  name: string
  itemType: 'game' | 'console' | 'accessory'
  platformName?: string
}

export type CatalogPick = GamePick | HardwarePick | PCListingPick | CommunityPick

interface SearchPickerProps {
  initialQuery?: string
  onPick: (pick: CatalogPick) => void
  footer?: ReactNode
  // Which search kinds to offer; the add wizard keeps the default,
  // the proxy picker adds the all-of-PriceCharting kind.
  kinds?: SearchKind[]
  // The community lane is shown by default (add wizard + admin adopt
  // surface it); the price proxy picker hides it, since community
  // products are priceless and cannot serve as a price source.
  communityLane?: 'shown' | 'hidden'
}

// pc_listing's label/noun is the PriceCharting brand name - a proper
// noun that never translates - so it stays a plain string while
// game/hardware translate; the two tables below therefore mix
// MessageDescriptor and string values.
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
// The search box's aria-label names the kinds on offer: "games and
// hardware" (default two kinds, pinned by the add wizard's tests and
// e2e steps) or an Oxford-comma list once PriceCharting joins in.
const kindNouns: Record<SearchKind, MessageDescriptor | string> = {
  game: msg`games`,
  hardware: msg`hardware`,
  pc_listing: 'PriceCharting',
}
function resolveKindText(v: MessageDescriptor | string, i18n: I18n): string {
  return typeof v === 'string' ? v : i18n._(v)
}
// searchBoxLabel is a plain function, not a component: it cannot call
// useLingui() itself, so its i18n comes from the caller's own hook
// (same reasoning as rowMeta - see components/collection/rowMeta.tsx)
// rather than the @lingui/core singleton, which would not force a
// re-render of SearchPicker on a later locale switch.
function searchBoxLabel(kinds: SearchKind[], i18n: I18n): string {
  const nouns = kinds.map((k) => resolveKindText(kindNouns[k], i18n))
  if (nouns.length < 2) return t(i18n)`Search for ${nouns.join('')}`
  if (nouns.length === 2) return t(i18n)`Search for ${nouns[0]} and ${nouns[1]}`
  return t(i18n)`Search for ${nouns.slice(0, -1).join(', ')}, and ${nouns[nouns.length - 1]}`
}

// SearchPicker is the shared catalog-search surface: the add wizard's
// first step and the pricing proxy picker. Picking a game means
// picking a platform (a product is game-on-platform); hardware and
// pc_listing picks are the listing itself.
export default function SearchPicker({ initialQuery = '', onPick, footer, kinds = ['game', 'hardware'], communityLane = 'shown' }: SearchPickerProps) {
  const { t, i18n } = useLingui()
  const money = useDisplayMoney()
  const [kind, setKind] = useState<SearchKind>(kinds[0])
  const [text, setText] = useState(initialQuery)
  const [submitted, setSubmitted] = useState(initialQuery.trim())

  const search = useQuery({
    queryKey: ['search', kind, submitted],
    queryFn: () => searchCatalog(kind, submitted),
    enabled: submitted !== '',
  })
  // The hidden-lane filter drops community rows client-side (ProxyPicker's
  // price-source picker); the "no results" message below must react to
  // this filtered list too, or an all-community answer would render
  // neither the message nor any rows.
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
        }}
        className="flex flex-wrap items-center gap-2"
      >
        {kinds.length > 1 && (
          <fieldset className="flex gap-2" aria-label={t`Search type`}>
            {kinds.map((k) => (
              <label key={k} className="flex items-center gap-1 text-sm">
                <input type="radio" name="kind" checked={kind === k} onChange={() => setKind(k)} />
                {resolveKindText(kindLabels[k], i18n)}
              </label>
            ))}
          </fieldset>
        )}
        <input
          type="search"
          aria-label={searchBoxLabel(kinds, i18n)}
          value={text}
          onChange={(e) => setText(e.target.value)}
          placeholder={i18n._(kindPlaceholders[kind])}
          className="w-64 rounded border border-gray-300 px-2 py-1 text-sm"
        />
        <button
          type="submit"
          disabled={text.trim() === ''}
          className="rounded bg-gray-900 px-3 py-1 text-sm text-white enabled:hover:bg-gray-700 disabled:opacity-50"
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
          <li key={r.product_id ?? i} className="flex items-start gap-3 rounded border border-gray-200 p-2">
            {r.cover_url ? (
              <img src={r.cover_url} alt="" className="h-16 w-auto rounded" />
            ) : (
              <div aria-hidden="true" className="flex h-16 w-12 shrink-0 items-center justify-center rounded bg-gray-100 text-gray-400">
                <ItemTypeIcon
                  type={
                    r.origin === 'community'
                      ? (r.item_type ?? 'game')
                      : r.type === 'game'
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
                {r.origin === 'community' && (
                  <span className="ml-2 rounded bg-indigo-100 px-1.5 py-0.5 text-xs font-semibold text-indigo-800">
                    <Trans>community</Trans>
                  </span>
                )}
              </p>
              {r.origin === 'community' ? (
                r.platform_name ? (
                  <p className="mt-1 flex flex-wrap items-center gap-1">
                    {/* Mirrors the provider game row's chip idiom below:
                        the chip is the pick target, not the row. */}
                    <span className="text-xs text-gray-500"><Trans>Add on:</Trans></span>
                    <button
                      type="button"
                      onClick={() =>
                        onPick({
                          kind: 'community',
                          productId: r.product_id!,
                          name: r.name,
                          itemType: r.item_type ?? 'game',
                          platformName: r.platform_name,
                        })
                      }
                      aria-label={t`${r.name} on ${r.platform_name}`}
                      className="rounded border border-gray-300 px-2 py-0.5 text-xs hover:border-gray-400 hover:bg-gray-50"
                    >
                      {r.platform_name}
                    </button>
                  </p>
                ) : (
                  <button
                    type="button"
                    onClick={() =>
                      onPick({
                        kind: 'community',
                        productId: r.product_id!,
                        name: r.name,
                        itemType: r.item_type ?? 'game',
                        platformName: r.platform_name,
                      })
                    }
                    className="mt-1 rounded border border-gray-300 px-2 py-0.5 text-xs hover:border-gray-400 hover:bg-gray-50"
                  >
                    <Trans>Add {r.name}</Trans>
                  </button>
                )
              ) : r.type === 'game' && r.igdb_game_id !== undefined ? (
                <p className="mt-1 flex flex-wrap items-center gap-1">
                  {/* The chips are the pick targets, not the row; say so. */}
                  <span className="text-xs text-gray-500"><Trans>Add on:</Trans></span>
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
                      aria-label={t`${r.name} on ${p.name}`}
                      className="rounded border border-gray-300 px-2 py-0.5 text-xs hover:border-gray-400 hover:bg-gray-50"
                    >
                      {p.name}
                    </button>
                  ))}
                </p>
              ) : r.type === 'pc_listing' && r.pc_product_id !== undefined ? (
                <div className="mt-1 flex flex-col gap-1">
                  <p className="text-xs text-gray-500">
                    <Trans>
                      Loose {money.format(r.loose_cents) ?? '-'} / CIB {money.format(r.cib_cents) ?? '-'} / New{' '}
                      {money.format(r.new_cents) ?? '-'}
                    </Trans>
                  </p>
                  <button
                    type="button"
                    onClick={() =>
                      onPick({ kind: 'pc_listing', pcProductId: r.pc_product_id!, name: r.name })
                    }
                    className="self-start rounded border border-gray-300 px-2 py-0.5 text-xs hover:border-gray-400 hover:bg-gray-50"
                  >
                    <Trans>Use {r.name}</Trans>
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
                  <Trans>Add {r.name}</Trans>
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
