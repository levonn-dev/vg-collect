import type { ResolveRequest } from '../api/catalog'
import type { CatalogPick } from '../components/catalog/SearchPicker'

// A manual match is the user's exact PriceCharting listing choice for
// a game being added: it rides the resolve so the product mints with
// that mapping (or fills an existing product's missing one) instead
// of auto-match.
export interface ManualMatch {
  pcProductId: number
  name: string
}

// resolveRequestFor turns a catalog pick into the request that finds
// or creates its canonical product. PriceCharting's Systems category
// maps to console; everything else it lists for hardware
// (controllers, accessories) is an accessory.
export function resolveRequestFor(pick: CatalogPick, manualMatch?: ManualMatch | null): ResolveRequest {
  if (pick.kind === 'game') {
    return {
      type: 'game',
      igdb_game_id: pick.igdbGameId,
      platform_igdb_id: pick.platformId,
      ...(manualMatch ? { pc_product_id: manualMatch.pcProductId } : {}),
    }
  }
  if (pick.kind === 'pc_listing') {
    return { type: 'pc_listing', pc_product_id: pick.pcProductId }
  }
  return { type: pick.category === 'Systems' ? 'console' : 'accessory', pc_product_id: pick.pcProductId }
}
