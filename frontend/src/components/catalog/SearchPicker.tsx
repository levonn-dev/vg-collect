import { Trans, useLingui } from '@lingui/react/macro'
import { msg, t } from '@lingui/core/macro'
import type { I18n, MessageDescriptor } from '@lingui/core'
import { useQuery } from '@tanstack/react-query'
import type { ReactNode } from 'react'
import { useState } from 'react'
import type { SearchKind } from '../../api/catalog'
import { searchCatalog } from '../../api/catalog'
import { releaseYear } from '../../lib/format'
import type { EntryRegion } from '../../lib/productTitle'
import { bundleLang, consoleRegionFor, homeRegionFor, platformEntryRegions, REGION_FROM_MATCH, titleFormFor } from '../../lib/productTitle'
import { regionLabels, regionLabelText } from '../../lib/regionLabels'
import { useDisplayMoney } from '../../lib/useDisplayMoney'
import ItemTypeIcon from '../ItemTypeIcon'

export interface GamePick {
  kind: 'game'
  igdbGameId: number
  name: string
  platformId: number
  platformName: string
  // Platform-first region seeding: the matched-region mapping when the
  // clicked chip's own region set contains it, else the UI locale's
  // home region when the set contains that, else the chip's earliest
  // release region, else the matched mapping alone (unmappable chip),
  // else nothing (the wizard defaults ntsc_u).
  suggestedRegion?: EntryRegion | 'region_free'
  // The clicked chip's mapped region list (absent when it has none):
  // drives the details step's grouped Region select.
  regions?: EntryRegion[]
  // The result's localization bundles, verbatim: the details heading
  // derives the region-appropriate identity from them.
  localizations?: { region: string; name?: string; translit?: string; cover_url?: string }[]
  // The chip's own artwork and release date, carried through so a
  // based-add (CustomStep) can prefill the custom form's cover and
  // release date fields without a second lookup.
  coverUrl?: string
  firstReleaseDate?: string
}

export interface HardwarePick {
  kind: 'hardware'
  pcProductId: number
  name: string
  category: string
  // The listing's own region, derived from its console-name axis: a
  // PriceCharting listing prices exactly one region, so the wizard
  // seeds its region default with it.
  suggestedRegion: 'ntsc_u' | 'ntsc_j' | 'pal'
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
  // Same based-add prefill purpose as GamePick's fields above.
  coverUrl?: string
  firstReleaseDate?: string
  // The community facts region, entry vocabulary (open-world; see
  // regionLabelText) - seeds the wizard's region default the same way
  // suggestedRegion does for game/hardware picks.
  region?: string
}

export type CatalogPick = GamePick | HardwarePick | PCListingPick | CommunityPick

// The picker's full user-entered state, snapshotable by a caller that
// unmounts this component (the add wizard's step machine) and handed
// back as initialState so Back lands on the same query and results.
export interface SearchPickerState {
  kind: SearchKind
  text: string
  submitted: string
}

interface SearchPickerProps {
  initialQuery?: string
  // The last-reported onStateChange snapshot; wins over initialQuery
  // when present.
  initialState?: SearchPickerState
  onPick: (pick: CatalogPick) => void
  footer?: ReactNode
  // Which search kinds to offer; the add wizard keeps the default,
  // the proxy picker adds the all-of-PriceCharting kind.
  kinds?: SearchKind[]
  // The community lane is shown by default (add wizard + admin adopt
  // surface it); the price proxy picker hides it, since community
  // products are priceless and cannot serve as a price source.
  communityLane?: 'shown' | 'hidden'
  // Fires on every kind/text/submit change with the full next state,
  // so the caller's snapshot is always current.
  onStateChange?: (s: SearchPickerState) => void
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

// SearchPicker is the shared catalog-search surface: the add wizard's
// first step and the pricing proxy picker. Picking a game means
// picking a platform (a product is game-on-platform); hardware and
// pc_listing picks are the listing itself.
export default function SearchPicker({ initialQuery = '', initialState, onPick, footer, kinds = ['game', 'hardware'], communityLane = 'shown', onStateChange }: SearchPickerProps) {
  const { t, i18n } = useLingui()
  const money = useDisplayMoney()
  const [kind, setKind] = useState<SearchKind>(initialState?.kind ?? kinds[0])
  const [text, setText] = useState(initialState?.text ?? initialQuery)
  const [submitted, setSubmitted] = useState(initialState?.submitted ?? initialQuery.trim())
  const report = (next: Partial<SearchPickerState>) => onStateChange?.({ kind, text, submitted, ...next })

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
        {rows.map((r, i) => {
          const name = r.name
          const platformName = r.platform_name
          const loose = money.format(r.loose_cents) ?? '-'
          const cib = money.format(r.cib_cents) ?? '-'
          const newPrice = money.format(r.new_cents) ?? '-'
          // Region-localized presentation (game results only): a
          // matched_region hit picks its bundle and swaps the title,
          // secondary name, and cover; a plain canonical-name match
          // (no matched_region) renders r.name/r.cover_url untouched
          // even when localizations are present, so browsing never
          // flips a title the query did not ask for.
          const matchedBundle = r.matched_region
            ? r.localizations?.find((l) => l.region === r.matched_region)
            : undefined
          const form = titleFormFor(i18n.locale)
          const title = matchedBundle
            ? (form === 'native'
                ? (matchedBundle.name ?? matchedBundle.translit ?? r.name)
                : (matchedBundle.translit ?? matchedBundle.name ?? r.name))
            : r.name
          // bundleLang is undefined for a continent-form identifier
          // (EU); guarded here so a translit title never renders the
          // literal string "undefined-Latn".
          const bundleLangTag = matchedBundle ? bundleLang(matchedBundle.region) : undefined
          const titleLang = matchedBundle && title !== r.name
            ? (title === matchedBundle.name ? bundleLangTag : (bundleLangTag ? `${bundleLangTag}-Latn` : undefined))
            : undefined
          const cover = matchedBundle?.cover_url ?? r.cover_url
          return (
            <li key={r.product_id ?? i} className="flex items-start gap-3 rounded border border-gray-200 p-2">
              {cover ? (
                <img src={cover} alt="" className="h-16 w-auto rounded" />
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
                  <span lang={titleLang}>{title}</span>
                  {title !== r.name && <span className="ml-2 text-xs text-gray-400">{r.name}</span>}
                  {releaseYear(r.first_release_date) && (
                    <span className="ml-2 text-xs text-gray-400">{releaseYear(r.first_release_date)}</span>
                  )}
                  {r.console_name && (
                    <span className="ml-2 text-xs text-gray-400">
                      {r.console_name}
                      {(r.type === 'hardware' || r.type === 'pc_listing') && (
                        <span className="ml-1">{i18n._(regionLabels[consoleRegionFor(r.console_name)])}</span>
                      )}
                    </span>
                  )}
                  {r.category && <span className="ml-2 text-xs text-gray-400">{r.category}</span>}
                  {r.origin === 'community' && (
                    <span className="ml-2 rounded bg-indigo-100 px-1.5 py-0.5 text-xs font-semibold text-indigo-800">
                      <Trans>community</Trans>
                    </span>
                  )}
                  {r.region !== undefined && (
                    <span className="ml-2 text-xs text-gray-400">{regionLabelText(i18n, r.region)}</span>
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
                            coverUrl: r.cover_url,
                            firstReleaseDate: r.first_release_date,
                            region: r.region,
                          })
                        }
                        aria-label={t`${name} on ${platformName}`}
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
                          coverUrl: r.cover_url,
                          firstReleaseDate: r.first_release_date,
                          region: r.region,
                        })
                      }
                      className="mt-1 rounded border border-gray-300 px-2 py-0.5 text-xs hover:border-gray-400 hover:bg-gray-50"
                    >
                      <Trans>Add {name}</Trans>
                    </button>
                  )
                ) : r.type === 'game' && r.igdb_game_id !== undefined ? (
                  <p className="mt-1 flex flex-wrap items-center gap-1">
                    {/* The chips are the pick targets, not the row; say so. */}
                    <span className="text-xs text-gray-500"><Trans>Add on:</Trans></span>
                    {r.platforms?.map((p) => {
                      const platformName = p.name
                      // The chips are the region picker: each shows its platform's full
                      // mapped region list (worldwide expands - a picker shows the whole
                      // choice set), and the pick seeds the wizard platform-first: the
                      // matched-region mapping wins within the chip's set, else the UI
                      // locale's home region within the set, else the earliest release
                      // region. The shadow of the outer platformName is the file's chip
                      // idiom.
                      const regions = platformEntryRegions(p.release_regions)
                      const regionText = regions.map((b) => i18n._(regionLabels[b])).join('/')
                      const matched = r.matched_region ? REGION_FROM_MATCH[r.matched_region] : undefined
                      const home = homeRegionFor(i18n.locale)
                      const suggestedRegion =
                        matched !== undefined && regions.some((x) => x === matched) ? matched
                        : home !== undefined && regions.includes(home) ? home
                        : regions.length > 0 ? regions[0]
                        : matched
                      return (
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
                              suggestedRegion,
                              regions: regions.length > 0 ? regions : undefined,
                              localizations: r.localizations,
                              coverUrl: r.cover_url,
                              firstReleaseDate: r.first_release_date,
                            })
                          }
                          aria-label={
                            regions.length > 0
                              ? t`${name} on ${platformName} (${regionText})`
                              : t`${name} on ${platformName}`
                          }
                          className="rounded border border-gray-300 px-2 py-0.5 text-xs hover:border-gray-400 hover:bg-gray-50"
                        >
                          {p.name}
                          {regions.length > 0 && <span className="ml-1 text-gray-400">{regionText}</span>}
                        </button>
                      )
                    })}
                  </p>
                ) : r.type === 'pc_listing' && r.pc_product_id !== undefined ? (
                  <div className="mt-1 flex flex-col gap-1">
                    <p className="text-xs text-gray-500">
                      <Trans>
                        Loose {loose} / CIB {cib} / New{' '}
                        {newPrice}
                      </Trans>
                    </p>
                    <button
                      type="button"
                      onClick={() =>
                        onPick({ kind: 'pc_listing', pcProductId: r.pc_product_id!, name: r.name })
                      }
                      className="self-start rounded border border-gray-300 px-2 py-0.5 text-xs hover:border-gray-400 hover:bg-gray-50"
                    >
                      <Trans>Use {name}</Trans>
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
                        suggestedRegion: consoleRegionFor(r.console_name ?? ''),
                      })
                    }
                    className="mt-1 rounded border border-gray-300 px-2 py-0.5 text-xs hover:border-gray-400 hover:bg-gray-50"
                  >
                    <Trans>Add {name}</Trans>
                  </button>
                ) : null}
              </div>
            </li>
          )
        })}
      </ul>
      {footer}
    </section>
  )
}
