import { expect, test } from '@playwright/test'
import type { Page } from '@playwright/test'

// Admin console journey as the dev fixture admin. task e2e grants the
// admin role before Playwright starts, and the login below is fresh,
// which is what puts the role in the JWT.
//
// Determinism note: the add wizard does not carry the "Edition or
// variant" text into the PRODUCT. Console resolve keys identity on
// (pc_product_id, region, edition, variant) and the wizard sends only
// pc_product_id (see resolveRequestFor), so the stamp lands on the
// ENTRY - giving each run a unique, self-cleaning entry - while the
// product converges on one shared identity. Product-level determinism
// therefore comes from the clear -> re-map round trip restoring the
// mapping every run (the same trick the bruno admin flow uses), not
// from a per-run product family. The product id AND name are captured
// from the resolve response so the spec follows whatever gamecube
// variant the live search actually returns rather than assuming one.
const stamp = `e2e-admin-${Date.now()}`

// Programmatic dev-provider login: one GET seals the session cookie and
// redirects home, a single /api/auth/* hit (the old /login UI helper cost
// two). The gateway caps /api/auth/* at 20 per 60s per IP across the
// shared serial suite; one hit per login keeps every window well under it.
async function login(page: Page, fixture: string) {
  await page.goto(`/api/auth/login?provider=dev&user=${fixture}`)
  await expect(page.getByRole('navigation', { name: 'Primary' })).toBeVisible()
}

// Accept the next native confirm dialog once (clear and delete both ask).
function acceptNext(page: Page) {
  page.once('dialog', (d) => void d.accept())
}

test('non-admin never sees the admin surface', async ({ page }) => {
  await login(page, 'bob')
  await expect(page.getByRole('link', { name: 'Admin' })).toHaveCount(0)
  // A deep link renders nothing admin-shaped either: the page guard
  // redirects home (the server would answer 403 regardless), which
  // then resolves through the landing-page redirect to /feed.
  await page.goto('/admin')
  await expect(page).toHaveURL('/feed')
})

test('admin fixes a cleared mapping end to end', async ({ page }) => {
  test.setTimeout(120_000)

  await login(page, 'admin')
  await expect(page.getByRole('link', { name: 'Admin' })).toBeVisible()

  // --- Create the fixture hardware entry through the add wizard,
  // capturing the resolved product's id and name. first(): real
  // hardware search returns colored and bundled variants, and the only
  // bare "Add Gamecube System" hit is a bracketed edition listing, so
  // the resolved product name is not a plain "Gamecube System".
  await page.getByRole('link', { name: 'Add', exact: true }).click()
  await page.getByRole('radio', { name: /hardware/i }).check()
  await page.getByRole('searchbox', { name: /search for games and hardware/i }).fill('gamecube system')
  await page.getByRole('button', { name: 'Search', exact: true }).click()
  await page.getByRole('button', { name: /Add Gamecube System/ }).first().click()
  await page.getByLabel('Edition or variant').fill(stamp)
  // The wizard resolves on the confirm step's mount - right after
  // Continue - so arm the wait first. Find and create both answer 200
  // through the same product writer.
  const resolveDone = page.waitForResponse(
    (r) => r.url().includes('/api/products/resolve') && r.request().method() === 'POST' && r.status() === 200,
  )
  await page.getByRole('button', { name: 'Continue' }).click()
  const resolved = (await (await resolveDone).json()) as { id: string; name: string }
  const productId = resolved.id
  const productName = resolved.name
  await page.getByRole('button', { name: 'Add to collection' }).click()
  await expect(page).toHaveURL(/\/entries\//)
  const entryURL = page.url()

  // --- Look the product up on the admin page and clear its mapping.
  await page.getByRole('link', { name: 'Admin', exact: true }).click()
  await expect(page.getByRole('heading', { name: 'Admin' })).toBeVisible()
  await expect(page.getByText(/\d+ unmatched products/)).toBeVisible()

  const lookup = page.getByRole('region', { name: 'Product lookup' })
  await lookup.getByRole('textbox', { name: 'Product id' }).fill(productId)
  await lookup.getByRole('button', { name: 'Look up' }).click()
  // A mapped product resolved: Clear exists only when a mapping is
  // present, so its visibility is the "found it, and it is mapped"
  // signal. The name shows in two places here, so asserting it by text
  // would be ambiguous under strict mode.
  await expect(lookup.getByRole('button', { name: 'Clear mapping' })).toBeVisible()

  acceptNext(page)
  await lookup.getByRole('button', { name: 'Clear mapping' }).click()
  // The clear repaints the same panel unmatched and held.
  await expect(lookup.getByText('held')).toBeVisible()
  await expect(lookup.getByText(/unmatched/i)).toBeVisible()

  // --- The cleared product surfaces in the worklist, badged held. The
  // stamp is an entry fact, not a product one, so the row is found by
  // the product name; "held" marks the deliberate clear this run just
  // made, so in a clean stack this is the one held row and it is ours
  // (the round trip below restores the mapping, keeping re-runs clean).
  const worklist = page.getByRole('region', { name: 'Unmatched products' })
  const fixtureRow = worklist.locator('tbody tr').filter({ hasText: productName }).filter({ hasText: 'held' })
  await expect(fixtureRow).toHaveCount(1)

  // --- Fix it from the worklist through the listing picker.
  await fixtureRow.getByRole('button', { name: 'Fix mapping' }).click()
  // MappingFix opens for our product (its aria-label region) inside the
  // worklist; only one fix panel is open at a time.
  const fixPanel = worklist.locator(`[aria-label="Fix mapping for ${productName}"]`)
  await expect(fixPanel).toBeVisible()
  await fixPanel.getByRole('button', { name: 'Choose listing' }).click()
  const matchDialog = page.getByRole('dialog', { name: 'Match a price listing' })
  await matchDialog.getByRole('searchbox', { name: 'Search for PriceCharting' }).fill(productName)
  await matchDialog.getByRole('button', { name: 'Search', exact: true }).click()
  // The listing name equals the product name for hardware, so the pick
  // is name-exact; first() guards against regional prints of the same
  // listing surfacing more than once.
  await matchDialog.getByRole('button', { name: `Use ${productName}`, exact: true }).first().click()

  // The re-set lifts the hold and the row leaves the worklist (a
  // matched product is not unmatched). Wait out the moderated set's
  // provider round trip and the list refetch.
  await expect(
    worklist.locator('tbody tr').filter({ hasText: productName }).filter({ hasText: 'held' }),
  ).toHaveCount(0, { timeout: 30_000 })

  // --- Cleanup: delete the fixture entry. The catalog product persists
  // by design, mapped again, which is exactly what the next run finds.
  // Mirrors the entry delete at the end of the collection journey.
  await page.goto(entryURL)
  acceptNext(page)
  await page.getByRole('button', { name: 'Delete entry' }).click()
  await expect(page).toHaveURL(/\/collection$/)
})
