import type { ResolveRequest } from '../api/catalog'
import type { CatalogPick } from '../components/catalog/SearchPicker'

// resolveRequestFor turns a catalog pick into the request that finds
// or creates its canonical product. PriceCharting's Systems category
// maps to console; everything else it lists for hardware
// (controllers, accessories) is an accessory.
export function resolveRequestFor(pick: CatalogPick): ResolveRequest {
  if (pick.kind === 'game') {
    return { type: 'game', igdb_game_id: pick.igdbGameId, platform_igdb_id: pick.platformId }
  }
  if (pick.kind === 'pc_listing') {
    return { type: 'pc_listing', pc_product_id: pick.pcProductId }
  }
  return { type: pick.category === 'Systems' ? 'console' : 'accessory', pc_product_id: pick.pcProductId }
}
