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
    resolveRequestFor({ kind: 'hardware', pcProductId: 6001, name: 'SNES', category: 'Systems' }, { pcProductId: 7042, name: 'Y' }),
  ).toEqual({ type: 'console', pc_product_id: 6001 })
})
