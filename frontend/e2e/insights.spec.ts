import type { APIRequestContext } from '@playwright/test'
import { expect, loginAs, test } from './fixtures'
import { createEntry, resolveProduct, updateEntry } from './seed'

// Both tests assert user-global aggregates, so each mints its own
// freshUser; teardown deletes the user and its entries.
const stamp = `e2e-insights-${Date.now()}`

// IGDB_MODE can point the stack at the stub or real catalog, which
// disagree on igdb_game_id; a live search finds the right id either way
// (same as resolveRequestFor's caller, the add wizard).
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
  // custom_value_set_at (server-stamped) gives ComposeValueSeries a
  // point independent of any priced entry, so the series is never empty.
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
  // Rating 9 lifts owner weight to 2.0 (recs.ownerWeight); every
  // similar_games entry is unowned by a fresh user, so CandidateIDs
  // finds a direct edge and Score ranks above zero without a fallback.
  await createEntry(fresh.api, { product_id: product.id, status: 'beaten', rating: 9 })

  await loginAs(page, fresh.name)
  await page.getByRole('link', { name: 'Recommendations', exact: true }).click()
  await expect(page.getByRole('main', { name: 'Recommendations' })).toBeVisible()
  await expect(page.getByRole('link', { name: /to collection/ }).first()).toBeVisible()
})
