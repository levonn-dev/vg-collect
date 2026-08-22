import type { CatalogPick } from './catalogPicks'
import { resolveRequestFor } from './catalog'

it('builds a plain game resolve without a manual match', () => {
  expect(
    resolveRequestFor({ kind: 'game', igdbGameId: 1011, name: 'Chrono Trigger', platformId: 19, platformName: 'SNES' }),
  ).toEqual({ type: 'game', igdb_game_id: 1011, platform_igdb_id: 19 })
})

it('sends the manual match with a game resolve', () => {
  expect(
    resolveRequestFor(
      { kind: 'game', igdbGameId: 1011, name: 'Chrono Trigger', platformId: 19, platformName: 'SNES' },
      { pcProductId: 7042, name: 'Chrono Trigger [PAL]' },
    ),
  ).toEqual({ type: 'game', igdb_game_id: 1011, platform_igdb_id: 19, pc_product_id: 7042 })
})

it('ignores a manual match for non-game picks', () => {
  expect(
    resolveRequestFor({ kind: 'pc_listing', pcProductId: 5099, name: 'X' }, { pcProductId: 7042, name: 'Y' }),
  ).toEqual({ type: 'pc_listing', pc_product_id: 5099 })
  expect(
    resolveRequestFor(
      { kind: 'hardware', pcProductId: 6001, name: 'SNES', category: 'Systems', suggestedRegion: 'ntsc_u' },
      { pcProductId: 7042, name: 'Y' },
    ),
  ).toEqual({ type: 'console', pc_product_id: 6001 })
})

const gamePick: CatalogPick = { kind: 'game', igdbGameId: 1011, name: 'Chrono Trigger', platformId: 19, platformName: 'SNES' }
const listingPick: CatalogPick = { kind: 'pc_listing', pcProductId: 5099, name: 'X' }

it('threads a trimmed match hint on game picks', () => {
  const req = resolveRequestFor(gamePick, null, '  players choice  ')
  expect(req).toMatchObject({ type: 'game', match_hint: 'players choice' })
})

it('omits an empty or whitespace hint', () => {
  expect(resolveRequestFor(gamePick, null, '')).not.toHaveProperty('match_hint')
  expect(resolveRequestFor(gamePick, null, '   ')).not.toHaveProperty('match_hint')
  expect(resolveRequestFor(gamePick)).not.toHaveProperty('match_hint')
})

it('ignores the hint on non-game picks', () => {
  expect(resolveRequestFor(listingPick, null, 'players choice')).not.toHaveProperty('match_hint')
})

it('game resolves carry the entry region; picker and hardware kinds ignore it', () => {
  const game = { kind: 'game', igdbGameId: 1016, name: 'Secret of Mana', platformId: 19, platformName: 'SNES' } as const
  expect(resolveRequestFor(game, undefined, undefined, 'ntsc_j')).toMatchObject({ type: 'game', region: 'ntsc_j' })
  expect(resolveRequestFor(game, { pcProductId: 5101, name: 'x' }, undefined, 'ntsc_j')).toMatchObject({ pc_product_id: 5101, region: 'ntsc_j' })
  expect(resolveRequestFor(game)).not.toHaveProperty('region')
  const hardware = { kind: 'hardware', pcProductId: 6101, name: 'c', category: 'Systems', suggestedRegion: 'ntsc_j' } as const
  expect(resolveRequestFor(hardware, undefined, undefined, 'ntsc_j')).not.toHaveProperty('region')
})
