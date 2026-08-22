import type { APIRequestContext } from '@playwright/test'
import { expect, loginAs, test } from './fixtures'
import { createEntry, resolveProduct, updateEntry } from './seed'

// Two independent tests covering the insights surfaces: the panels
// folded into the collection page, and the dedicated recommendations
// page. Both assert user-global aggregates (collection-wide value
// history, the recommendation feed), so each mints its own freshUser;
// teardown deletes every entry and the user itself, so neither test
// cleans up by hand.
const stamp = `e2e-insights-${Date.now()}`

// The exact pick add-wizard.spec.ts's search tests drive through the
// search UI: Chrono Trigger on Super Nintendo Entertainment System. The
// enrichment service's provider switch (IGDB_MODE) can point this
// stack at the stub fixture catalog or the real IGDB catalog, and the
// two disagree on igdb_game_id (the stub's is a small fixture-only
// number; the real catalog assigns its own), so a hardcoded id resolves
// the wrong game - or none - depending on which is live. A live search
// finds the id either way, the same way resolveRequestFor's caller
// (the add wizard) always does.
async function resolveChronoTriggerSnes(api: APIRequestContext) {
  const res = await api.get('/api/search', { params: { type: 'game', q: 'chrono trigger' } })
  expect(res.ok(), `search chrono trigger: ${res.status()}`).toBeTruthy()
  const body = (await res.json()) as {
    results: { igdb_game_id: number; name: string; platforms?: { igdb_platform_id: number; name: string }[] }[]
  }
  const hit = body.results.find(
    (r) => r.name === 'Chrono Trigger' && r.platforms?.some((p) => p.name === 'Super Nintendo Entertainment System'),
  )
  const platform = hit?.platforms?.find((p) => p.name === 'Super Nintendo Entertainment System')
  expect(platform, 'Chrono Trigger on Super Nintendo Entertainment System not found in search').toBeTruthy()
  return { type: 'game' as const, igdb_game_id: hit!.igdb_game_id, platform_igdb_id: platform!.igdb_platform_id }
}

test('insight panels ride the collection page', async ({ page, freshUser }) => {
  const fresh = await freshUser()
  const entry = await createEntry(fresh.api, { display_name: `Insights Custom ${stamp}` })
  // pricing_mode custom stamps custom_value_set_at server-side (first
  // set or on change), and ComposeValueSeries in the collection
  // service's handlers_dashboard.go turns that day into its own point
  // in the value-history series independent of any product-priced
  // entry - so the series is never empty for this user and the panel
  // renders (ValueOverTime.tsx returns a plain paragraph, no region,
  // while history.points is empty).
  await updateEntry(fresh.api, entry.id, { pricing_mode: 'custom', custom_value_cents: 12345 })

  await loginAs(page, fresh.name)
  await page.getByRole('link', { name: 'Collection', exact: true }).click()

  await expect(page.getByRole('region', { name: 'Totals' })).toBeVisible()
  await page.getByRole('button', { name: 'Show insights' }).click()
  await expect(page.getByRole('region', { name: 'By platform' })).toBeVisible()
  await expect(page.getByRole('region', { name: 'Collection value over time' })).toBeVisible()
  await expect(page.getByRole('region', { name: 'Recommendations' })).toBeVisible()
})

test('a beaten, rated game seeds recommendations', async ({ page, freshUser }) => {
  const fresh = await freshUser()
  const resolveReq = await resolveChronoTriggerSnes(fresh.api)
  const product = await resolveProduct(fresh.api, resolveReq)
  // A single beaten, highly-rated entry is enough to seed a row: rating
  // 9 lifts its owner weight to 2.0 (recs.ownerWeight), and every game
  // Chrono Trigger's own catalog metadata lists under similar_games is
  // unowned by a fresh user, so CandidateIDs finds at least one direct
  // edge and Score ranks it above zero without needing a second library
  // entry or the genre-profile fallback (enrichment's recs package).
  await createEntry(fresh.api, { product_id: product.id, status: 'beaten', rating: 9 })

  await loginAs(page, fresh.name)
  await page.getByRole('link', { name: 'Recommendations', exact: true }).click()
  await expect(page.getByRole('main', { name: 'Recommendations' })).toBeVisible()
  await expect(page.getByRole('link', { name: /to collection/ }).first()).toBeVisible()
})
