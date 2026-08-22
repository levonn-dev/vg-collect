import type { SearchKind, SearchResult } from '../api/catalog'
import type { EntryRegion } from './productTitle'

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
  // Curated credit lists, for based-add prefill.
  developers?: string[]
  publishers?: string[]
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

// communityPickOf turns a community search result into a CommunityPick.
// SearchResultRow's two community branches (platform-tagged and bare)
// both call it, so the payload they emit stays identical by
// construction instead of by copy-paste discipline between the two.
export function communityPickOf(result: SearchResult): CommunityPick {
  return {
    kind: 'community',
    productId: result.product_id!,
    name: result.name,
    itemType: result.item_type ?? 'game',
    platformName: result.platform_name,
    coverUrl: result.cover_url,
    firstReleaseDate: result.first_release_date,
    region: result.region,
    developers: result.developers,
    publishers: result.publishers,
  }
}
