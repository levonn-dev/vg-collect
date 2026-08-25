import type { SearchKind, SearchResult } from '../api/catalog'
import type { EntryRegion } from './productTitle'

export interface GamePick {
  kind: 'game'
  igdbGameId: number
  name: string
  platformId: number
  platformName: string
  // Region seeding precedence: matched-region mapping if in the chip's
  // set, else UI locale's home region if in the set, else chip's
  // earliest release, else the matched mapping alone, else nothing.
  suggestedRegion?: EntryRegion | 'region_free'
  // Clicked chip's mapped region list (absent if none); drives the
  // Region select's grouping.
  regions?: EntryRegion[]
  // Result's localization bundles verbatim; details heading derives
  // region-appropriate identity from them.
  localizations?: { region: string; name?: string; translit?: string; cover_url?: string }[]
  // Chip's artwork/release date, carried through so based-add
  // (CustomStep) can prefill without a second lookup.
  coverUrl?: string
  firstReleaseDate?: string
}

export interface HardwarePick {
  kind: 'hardware'
  pcProductId: number
  name: string
  category: string
  // Derived from the listing's console-name axis; a PC listing prices
  // exactly one region.
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
  // Entry-vocabulary region (open-world; regionLabelText); seeds the
  // default like suggestedRegion does.
  region?: string
  // Curated credit lists, for based-add prefill.
  developers?: string[]
  publishers?: string[]
}

export type CatalogPick = GamePick | HardwarePick | PCListingPick | CommunityPick

// Snapshotable so a caller that unmounts this (add wizard's step
// machine) can restore it as initialState on Back.
export interface SearchPickerState {
  kind: SearchKind
  text: string
  submitted: string
}

// Shared by SearchResultRow's two community branches, so their
// payload stays identical by construction.
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
