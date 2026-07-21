import type { ResolveRequest } from '../api/catalog'
import type { CatalogPick, CommunityPick } from '../components/catalog/SearchPicker'

// A manual match is the user's exact PriceCharting listing choice for
// a game being added: it rides the resolve, and because game identity
// is listing-keyed, the resolve lands on that listing's own product.
export interface ManualMatch {
  pcProductId: number
  name: string
}

// resolveRequestFor turns a catalog pick into the request that finds
// or creates its canonical product. PriceCharting's Systems category
// maps to console; everything else it lists for hardware
// (controllers, accessories) is an accessory. For games, matchHint is
// the typed edition-or-variant text: score-only, reweighting the
// auto-match without changing the search (omitted when blank).
// Community picks are excluded: they name an already-minted product,
// so callers fetch it directly instead of resolving.
export function resolveRequestFor(pick: Exclude<CatalogPick, CommunityPick>, manualMatch?: ManualMatch | null, matchHint?: string): ResolveRequest {
  if (pick.kind === 'game') {
    const hint = matchHint?.trim() ?? ''
    return {
      type: 'game',
      igdb_game_id: pick.igdbGameId,
      platform_igdb_id: pick.platformId,
      ...(manualMatch ? { pc_product_id: manualMatch.pcProductId } : {}),
      ...(hint !== '' ? { match_hint: hint } : {}),
    }
  }
  if (pick.kind === 'pc_listing') {
    return { type: 'pc_listing', pc_product_id: pick.pcProductId }
  }
  return { type: pick.category === 'Systems' ? 'console' : 'accessory', pc_product_id: pick.pcProductId }
}
