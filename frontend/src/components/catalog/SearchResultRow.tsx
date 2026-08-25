import { Trans, useLingui } from '@lingui/react/macro'
import type { SearchResult } from '../../api/catalog'
import type { CatalogPick } from '../../lib/catalogPicks'
import { communityPickOf } from '../../lib/catalogPicks'
import { releaseYear } from '../../lib/format'
import { bundleLang, consoleRegionFor, homeRegionFor, platformEntryRegions, REGION_FROM_MATCH, titleFormFor } from '../../lib/productTitle'
import { regionLabels, regionLabelText } from '../../lib/regionLabels'
import { useDisplayMoney } from '../../lib/useDisplayMoney'
import ItemTypeIcon from '../ItemTypeIcon'
import PriceTriple from '../PriceTriple'

interface SearchResultRowProps {
  result: SearchResult
  onPick: (pick: CatalogPick) => void
}

// One search-result card: cover/icon, region-localized title, origin-specific
// pick control.
export default function SearchResultRow({ result: r, onPick }: SearchResultRowProps) {
  const { t, i18n } = useLingui()
  const money = useDisplayMoney()
  const name = r.name
  const platformName = r.platform_name
  const loose = money.format(r.loose_cents) ?? '-'
  const cib = money.format(r.cib_cents) ?? '-'
  const newPrice = money.format(r.new_cents) ?? '-'
  // matched_region picks its bundle and swaps title/name/cover; a canonical-name
  // match renders r.name/cover untouched even with localizations present, so
  // browsing never flips a title the query didn't ask for.
  const matchedBundle = r.matched_region
    ? r.localizations?.find((l) => l.region === r.matched_region)
    : undefined
  const form = titleFormFor(i18n.locale)
  const title = matchedBundle
    ? (form === 'native'
        ? (matchedBundle.name ?? matchedBundle.translit ?? r.name)
        : (matchedBundle.translit ?? matchedBundle.name ?? r.name))
    : r.name
  // bundleLang is undefined for a continent-form identifier (EU); guarded so
  // a translit title never renders "undefined-Latn".
  const bundleLangTag = matchedBundle ? bundleLang(matchedBundle.region) : undefined
  const titleLang = matchedBundle && title !== r.name
    ? (title === matchedBundle.name ? bundleLangTag : (bundleLangTag ? `${bundleLangTag}-Latn` : undefined))
    : undefined
  const cover = matchedBundle?.cover_url ?? r.cover_url
  return (
    <li className="flex items-start gap-3 rounded border border-gray-200 p-2">
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
              {/* Mirrors the provider game row's chip idiom: chip is the pick target. */}
              <span className="text-xs text-gray-500"><Trans>Add on:</Trans></span>
              <button
                type="button"
                onClick={() => onPick(communityPickOf(r))}
                aria-label={t`${name} on ${platformName}`}
                className="rounded border border-gray-300 px-2 py-0.5 text-xs hover:border-gray-400 hover:bg-gray-50"
              >
                {r.platform_name}
              </button>
            </p>
          ) : (
            <button
              type="button"
              onClick={() => onPick(communityPickOf(r))}
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
              // Each chip shows its full mapped region list; suggestedRegion picks
              // within that set: matched_region first, else the UI locale's home
              // region, else the earliest release region.
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
            <PriceTriple loose={loose} cib={cib} newPrice={newPrice} className="text-xs text-gray-500" />
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
}
