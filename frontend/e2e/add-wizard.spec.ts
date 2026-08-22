import { acceptNext, entryIdFromURL, expect, test } from './fixtures'
import { deleteEntry } from './seed'

// Five independent arcs through the add wizard: search auto-match,
// manual price-listing match, hardware, custom-via-picker, and
// custom-based-on-a-catalog-pick. The worker fixture is already
// logged in via storageState, so every test opens straight on the
// home page with no login step, then heads to the Add link. Each
// test stamps what it creates and deletes it before finishing - test
// 1 owns the file's UI-delete coverage, tests 2-4 clean up through the
// api fixture, and test 5's verbatim port keeps its own UI delete too
// - so assertions stay scoped to each test's own stamped entry, never
// collection-wide counts.

test('search add auto-matches and prices', async ({ page }) => {
  const stamp = `e2e-add-${Date.now()}`
  await page.goto('/')

  // --- Add a game through search -> details -> match confirmation.
  // Picks are platform-exact so the journey is deterministic against
  // either provider mode: the stub catalog mirrors the real listings,
  // but real search returns many editions in provider order (the first
  // Chrono Trigger hit live is a PC port that prices nothing).
  await page.getByRole('link', { name: 'Add', exact: true }).click()
  await page.getByRole('searchbox', { name: /search for games and hardware/i }).fill('chrono trigger')
  await page.getByRole('button', { name: 'Search', exact: true }).click()
  await page.getByRole('button', { name: /Chrono Trigger on Super Nintendo Entertainment System/ }).first().click()

  await expect(page.getByRole('heading', { name: /your copy of chrono trigger/i })).toBeVisible()
  // The stamp lives in notes: typed edition text is a match hint now,
  // and a unique stamp would (correctly) land the add unmatched.
  await page.getByLabel('Notes').fill(stamp)
  await page.getByLabel('Status').selectOption('beaten')
  await page.getByLabel('Rating').selectOption('9')
  await page.getByRole('button', { name: 'Continue' }).click()

  // Chrono Trigger on SNES auto-matches in both provider modes (the
  // stub fixture mirrors the real listing), so the confirmation states
  // a match; a catalog change would surface here.
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

  // --- Add a second game, choosing its price listing manually.
  // Identity is listing-keyed: the picked listing lands the add on
  // that listing's OWN product, distinct from the auto-matched one -
  // no conflict, no admin note. Super Mario 64 carries a bracketed
  // variant listing in both provider modes, so the pick is name-exact
  // and deterministic against fresh AND long-lived stacks.
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
  // Name-exact + console-scoped (never a regional "PAL Nintendo 64"
  // row): the bracketed variant is present in both provider modes.
  // The console span nests its region tag ("Nintendo 64" + "NTSC-U"),
  // so exact text cannot match it; the anchored prefix keeps the
  // regional rows out and the exact button name disambiguates the rest.
  await matchDialog
    .locator('li')
    .filter({ has: page.getByText(/^Nintendo 64/) })
    .getByRole('button', { name: "Use Super Mario 64 [Player's Choice]", exact: true })
    .first()
    .click()
  await expect(page.getByRole('button', { name: 'Clear' })).toBeVisible()
  await page.getByRole('button', { name: 'Continue' }).click()

  // The confirm card names the picked listing's member: a manual pick
  // is exact by construction (match 100%), and picking a different
  // listing than auto-match is just a different product now.
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

  // --- Search the seeded catalog for a known title, then fall through
  // to a custom item instead of picking a search result directly - the
  // typed query rides along as the custom form's seed.
  await page.getByRole('link', { name: 'Add', exact: true }).click()
  await page.getByRole('searchbox', { name: /search for games and hardware/i }).fill('chrono trigger')
  await page.getByRole('button', { name: 'Search', exact: true }).click()
  await page.getByRole('button', { name: /add it as a custom item/i }).click()

  // --- Base the custom form on an existing item: the embedded picker
  // reuses that same query (CustomStep seeds it from the search text)
  // and auto-searches, so the pick below needs no second submit.
  // Chrono Trigger on SNES auto-matches in both provider modes (the
  // stub catalog mirrors the real listing), so the pick is
  // deterministic either way; .first() is defensive for the same reason.
  await page.getByRole('button', { name: 'Base on an existing item' }).click()
  const basePicker = page.getByRole('region', { name: 'Search' })
  await basePicker.getByRole('button', { name: /Chrono Trigger on Super Nintendo Entertainment System/ }).first().click()

  // --- The picked item's own facts replace the form wholesale, but
  // the name stays user-editable: it fills in from the pick, then gets
  // tweaked before creating.
  await expect(page.getByLabel('Name')).toHaveValue('Chrono Trigger')
  const basedName = `Based Add ${stamp}`
  await page.getByLabel('Name').fill(basedName)
  await page.getByRole('button', { name: 'Continue' }).click()
  await page.getByRole('button', { name: 'Continue' }).click()
  await page.getByRole('button', { name: 'Add to collection' }).click()
  await expect(page).toHaveURL(/\/entries\//)
  await expect(page.getByRole('heading', { name: basedName })).toBeVisible()

  // --- Cleanup: delete the entry (the file's pattern).
  acceptNext(page)
  await page.getByRole('button', { name: 'Delete entry' }).click()
  await expect(page).toHaveURL(/\/collection$/)
})
