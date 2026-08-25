import { acceptNext, expect, loginAs, test } from './fixtures'

// Worker fixture carries no admin role; the other two tests loginAs
// admin fresh (task e2e grants the role before Playwright starts, and
// a fresh login is required to put it in the JWT).

// Determinism: resolve keys on (pc_product_id, region, edition, variant)
// and the wizard sends only pc_product_id, so the stamp lands on the
// entry (self-cleaning) while the product converges on one shared id
// via the clear -> re-map round trip below.
const stamp = `e2e-admin-${Date.now()}`

test('non-admin never sees the admin surface', async ({ page }) => {
  await page.goto('/')
  await expect(page.getByRole('link', { name: 'Admin' })).toHaveCount(0)
  // Deep link redirects home (guard, then landing redirect to /feed);
  // server would 403 regardless.
  await page.goto('/admin')
  await expect(page).toHaveURL('/feed')
})

test('admin fixes a cleared mapping end to end', async ({ page }) => {
  test.setTimeout(120_000)

  await loginAs(page, 'admin')
  await expect(page.getByRole('link', { name: 'Admin' })).toBeVisible()

  // first(): search returns colored/bundled variants too; the bare
  // hit is a bracketed edition listing.
  await page.getByRole('link', { name: 'Add', exact: true }).click()
  await page.getByRole('radio', { name: /hardware/i }).check()
  await page.getByRole('searchbox', { name: /search for games and hardware/i }).fill('gamecube system')
  await page.getByRole('button', { name: 'Search', exact: true }).click()
  await page.getByRole('button', { name: /Add Gamecube System/ }).first().click()
  await page.getByLabel('Edition or variant').fill(stamp)
  // Resolves on confirm-step mount; arm the wait before clicking Continue.
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
  // Clear button visible only when mapped. Name appears twice here, so
  // assert by button, not text.
  await expect(lookup.getByRole('button', { name: 'Clear mapping' })).toBeVisible()

  acceptNext(page)
  await lookup.getByRole('button', { name: 'Clear mapping' }).click()
  // The clear repaints the same panel unmatched and held.
  await expect(lookup.getByText('held')).toBeVisible()
  await expect(lookup.getByText(/unmatched/i)).toBeVisible()

  // Stamp is entry-only, so find by product name + held badge; the
  // round trip below restores the mapping for re-runs.
  const worklist = page.getByRole('region', { name: 'Unmatched products' })
  const fixtureRow = worklist.locator('tbody tr').filter({ hasText: productName }).filter({ hasText: 'held' })
  await expect(fixtureRow).toHaveCount(1)

  // --- Fix it from the worklist through the listing picker.
  await fixtureRow.getByRole('button', { name: 'Fix mapping' }).click()
  // MappingFix opens in its own aria-label region; only one fix panel open at a time.
  const fixPanel = worklist.locator(`[aria-label="Fix mapping for ${productName}"]`)
  await expect(fixPanel).toBeVisible()
  await fixPanel.getByRole('button', { name: 'Choose listing' }).click()
  const matchDialog = page.getByRole('dialog', { name: 'Match a price listing' })
  await matchDialog.getByRole('searchbox', { name: 'Search for PriceCharting' }).fill(productName)
  await matchDialog.getByRole('button', { name: 'Search', exact: true }).click()
  // Listing name equals product name for hardware; first() guards
  // regional-print duplicates.
  await matchDialog.getByRole('button', { name: `Use ${productName}`, exact: true }).first().click()

  // Re-set lifts the hold; row leaves the worklist once matched.
  await expect(
    worklist.locator('tbody tr').filter({ hasText: productName }).filter({ hasText: 'held' }),
  ).toHaveCount(0, { timeout: 30_000 })

  // Product persists mapped by design; only the entry is cleaned up.
  await page.goto(entryURL)
  acceptNext(page)
  await page.getByRole('button', { name: 'Delete entry' }).click()
  await expect(page).toHaveURL(/\/collection$/)
})

test('the maintenance grid offers every lever', async ({ page }) => {
  await loginAs(page, 'admin')
  await page.goto('/admin')
  await expect(page.getByRole('heading', { name: 'Admin' })).toBeVisible()

  // Render-only: never clicks a lever. Firing one would rewrite
  // catalog-wide state parallel tests read their own captured values
  // from; catalog refresh is already exercised in submissions.spec.ts.
  const maintenance = page.getByRole('region', { name: 'Maintenance' })
  const leverTitles = [
    'Catalog refresh',
    'Entry rematch',
    'Entry resnapshot',
    'Normalize platforms',
    'Normalize regions',
    'Normalize community regions',
  ]
  for (const title of leverTitles) {
    await expect(maintenance.getByRole('heading', { name: title, level: 4, exact: true })).toBeVisible()
  }
})
