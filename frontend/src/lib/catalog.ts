import type { ResolveRequest } from '../api/catalog'
import type { CatalogPick, CommunityPick } from './catalogPicks'

// User's exact listing choice for a game add; game identity is
// listing-keyed, so resolve lands on that listing's own product.
export interface ManualMatch {
  pcProductId: number
  name: string
}

// PC's Systems category maps to console, everything else hardware maps
// to accessory. matchHint (games only) reweights auto-match without
// changing the search. Community picks excluded: already minted, fetched directly.
export function resolveRequestFor(pick: Exclude<CatalogPick, CommunityPick>, manualMatch?: ManualMatch | null, matchHint?: string, region?: string): ResolveRequest {
  if (pick.kind === 'game') {
    const hint = matchHint?.trim() ?? ''
    // region is the details-step entry region; the server uses it to steer auto-match and ignores it on the picker path.
    return {
      type: 'game',
      igdb_game_id: pick.igdbGameId,
      platform_igdb_id: pick.platformId,
      ...(manualMatch ? { pc_product_id: manualMatch.pcProductId } : {}),
      ...(hint !== '' ? { match_hint: hint } : {}),
      ...(region !== undefined ? { region } : {}),
    }
  }
  if (pick.kind === 'pc_listing') {
    return { type: 'pc_listing', pc_product_id: pick.pcProductId }
  }
  return { type: pick.category === 'Systems' ? 'console' : 'accessory', pc_product_id: pick.pcProductId }
}
