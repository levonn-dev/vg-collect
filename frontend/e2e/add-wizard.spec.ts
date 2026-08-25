import { acceptNext, entryIdFromURL, expect, test } from './fixtures'
import { deleteEntry } from './seed'

// Worker fixture is already logged in via storageState; every test
// goes straight to Add. Tests 1 and 5 keep UI-delete coverage; tests
// 2-4 delete via the api fixture.

test('search add auto-matches and prices', async ({ page }) => {
  const stamp = `e2e-add-${Date.now()}`
  await page.goto('/')

  // Platform-exact pick keeps this deterministic against real search's
  // edition order (its first hit live is a non-pricing PC port).
  await page.getByRole('link', { name: 'Add', exact: true }).click()
  await page.getByRole('searchbox', { name: /search for games and hardware/i }).fill('chrono trigger')
  await page.getByRole('button', { name: 'Search', exact: true }).click()
  await page.getByRole('button', { name: /Chrono Trigger on Super Nintendo Entertainment System/ }).first().click()

  await expect(page.getByRole('heading', { name: /your copy of chrono trigger/i })).toBeVisible()
  // Stamp lives in notes: typed edition text is a match hint, and a
  // unique stamp would land the add unmatched.
  await page.getByLabel('Notes').fill(stamp)
  await page.getByLabel('Status').selectOption('beaten')
  await page.getByLabel('Rating').selectOption('9')
  await page.getByRole('button', { name: 'Continue' }).click()

  // Auto-matches in both provider modes (stub mirrors real listing); a
  // catalog change would surface here.
  await expect(page.getByText(/match \d+%/i)).toBeVisible()
  await page.getByRole('button', { name: 'Add to collection' }).click()
  await expect(page.getByRole('heading', { name: 'Chrono Trigger' })).toBeVisible()
  await expect(page.getByRole('region', { name: 'Pricing' })).toBeVisible()
  await expect(page).toHaveURL(/\/entries\//)

  // Cleanup through the UI (see file header - this test owns it).
  acceptNext(page)
  await page.getByRole('button', { name: 'Delete entry' }).click()
  await expect(page).toHaveURL(/\/collection$/)
})

test("manual listing match lands the pick's own product", async ({ page, api }) => {
  const stamp = `e2e-add-${Date.now()}`
  await page.goto('/')

  // Listing-keyed identity: picked listing lands on its own product,
  // no conflict. Bracketed variant exists in both provider modes, so
  // the name-exact pick is deterministic.
  await page.getByRole('link', { name: 'Add', exact: true }).click()
  await page.getByRole('searchbox', { name: /search for games and hardware/i }).fill('super mario 64')
  await page.getByRole('button', { name: 'Search', exact: true }).click()
  await page.getByRole('button', { name: /Super Mario 64 on Nintendo 64/ }).first().click()

  await expect(page.getByRole('heading', { name: /your copy of super mario 64/i })).toBeVisible()
  await page.getByLabel('Notes').fill(`${stamp} manual`)
  await page.getByRole('button', { name: 'Match manually' }).click()
  const matchDialog = page.getByRole('dialog', { name: 'Match a price listing' })
  await expect(matchDialog).toBeVisible()
  await matchDialog.getByRole('searchbox', { name: 'Search for PriceCharting' }).fill('super mario 64')
  await matchDialog.getByRole('button', { name: 'Search', exact: true }).click()
  // Console span nests its region tag, so exact text can't match the
  // row directly; anchored prefix excludes regional rows, exact button
  // name disambiguates the rest.
  await matchDialog
    .locator('li')
    .filter({ has: page.getByText(/^Nintendo 64/) })
    .getByRole('button', { name: "Use Super Mario 64 [Player's Choice]", exact: true })
    .first()
    .click()
  await expect(page.getByRole('button', { name: 'Clear' })).toBeVisible()
  await page.getByRole('button', { name: 'Continue' }).click()

  // Manual pick is exact by construction (match 100%); a different
  // listing means a different product.
  await expect(page.getByText('Priced as "Super Mario 64 [Player\'s Choice]"', { exact: false })).toBeVisible()
  await expect(page.getByText(/match 100%/i)).toBeVisible()
  await page.getByRole('button', { name: 'Add to collection' }).click()
  await expect(page.getByRole('region', { name: 'Pricing' })).toBeVisible()
  await expect(page).toHaveURL(/\/entries\//)

  await deleteEntry(api, entryIdFromURL(page.url()))
})

test('hardware add', async ({ page, api }) => {
  const stamp = `e2e-add-${Date.now()}`
  await page.goto('/')

  // --- Add a console through hardware search.
  await page.getByRole('link', { name: 'Add', exact: true }).click()
  await page.getByRole('radio', { name: /hardware/i }).check()
  await page.getByRole('searchbox', { name: /search for games and hardware/i }).fill('gamecube system')
  await page.getByRole('button', { name: 'Search', exact: true }).click()
  // first(): real hardware search also returns regional listings.
  await page.getByRole('button', { name: /Add Gamecube System/ }).first().click()
  await page.getByLabel('Edition or variant').fill(stamp)
  await page.getByLabel('Status').selectOption('shelved')
  await page.getByRole('button', { name: 'Continue' }).click()
  await page.getByRole('button', { name: 'Add to collection' }).click()
  await expect(page.getByRole('heading', { name: 'Gamecube System' })).toBeVisible()
  await expect(page).toHaveURL(/\/entries\//)

  await deleteEntry(api, entryIdFromURL(page.url()))
})

test('custom add through the platform picker', async ({ page, api }) => {
  const stamp = `e2e-add-${Date.now()}`
  const name = `Custom Picker ${stamp}`
  await page.goto('/')

  await page.getByRole('link', { name: 'Add', exact: true }).click()
  await page.getByRole('button', { name: /custom item/i }).click()
  await page.getByLabel('Name').fill(name)
  await page.getByLabel('Platform').fill('snes')
  await page.getByRole('button', { name: 'Super Nintendo Entertainment System' }).click()
  await page.getByRole('button', { name: 'Continue' }).click()
  await page.getByRole('button', { name: 'Continue' }).click() // details defaults: backlog
  await expect(page.getByText(/start without market pricing/i)).toBeVisible()
  await page.getByRole('button', { name: 'Add to collection' }).click()
  await expect(page).toHaveURL(/\/entries\//)

  await deleteEntry(api, entryIdFromURL(page.url()))
})

test('based-add seeds the custom form from a catalog pick', async ({ page }) => {
  test.setTimeout(60_000)
  const stamp = `e2e-add-${Date.now()}`
  await page.goto('/')

  // Falls through to custom instead of picking a result; typed query
  // rides along as the custom form's seed.
  await page.getByRole('link', { name: 'Add', exact: true }).click()
  await page.getByRole('searchbox', { name: /search for games and hardware/i }).fill('chrono trigger')
  await page.getByRole('button', { name: 'Search', exact: true }).click()
  await page.getByRole('button', { name: /add it as a custom item/i }).click()

  // Embedded picker reuses the search query (CustomStep) and
  // auto-searches, so no second submit needed. Auto-matches in both
  // provider modes; first() is defensive for the same reason.
  await page.getByRole('button', { name: 'Base on an existing item' }).click()
  const basePicker = page.getByRole('region', { name: 'Search' })
  await basePicker.getByRole('button', { name: /Chrono Trigger on Super Nintendo Entertainment System/ }).first().click()

  // Picked item's facts replace the form wholesale, but name stays user-editable.
  await expect(page.getByLabel('Name')).toHaveValue('Chrono Trigger')
  const basedName = `Based Add ${stamp}`
  await page.getByLabel('Name').fill(basedName)
  await page.getByRole('button', { name: 'Continue' }).click()
  await page.getByRole('button', { name: 'Continue' }).click()
  await page.getByRole('button', { name: 'Add to collection' }).click()
  await expect(page).toHaveURL(/\/entries\//)
  await expect(page.getByRole('heading', { name: basedName })).toBeVisible()

  // Delete the entry (test 1's pattern).
  acceptNext(page)
  await page.getByRole('button', { name: 'Delete entry' }).click()
  await expect(page).toHaveURL(/\/collection$/)
})
