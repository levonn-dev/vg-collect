import { expect, test } from '@playwright/test'
import type { Page } from '@playwright/test'

// One serial journey as the dev fixture bob. Every object it creates
// carries the unique stamp and is deleted at the end, so the suite
// stays re-runnable against a long-lived stack and never collides
// with data from earlier runs or other tools.
const stamp = `e2e-${Date.now()}`
const customA = `Repro Alpha ${stamp}`
const customB = `Repro Beta ${stamp}`
const viewName = `View ${stamp}`

async function login(page: Page) {
  await page.goto('/login')
  await page.getByRole('link', { name: 'bob', exact: true }).click()
  await expect(page.getByRole('navigation', { name: 'Primary' })).toBeVisible()
}

// Accept the next native dialog (confirm or prompt) once.
function acceptNext(page: Page, promptText?: string) {
  page.once('dialog', (d) => void d.accept(promptText))
}

test('collection journey: add, edit, price, pin, reorder, views, dashboard, recommendations', async ({ page }) => {
  test.setTimeout(180_000)
  const createdEntryURLs: string[] = []
  await login(page)

  // --- Add a game through search -> details -> match confirmation.
  // Picks are platform-exact so the journey is deterministic against
  // either provider mode: the stub catalog mirrors the real listings,
  // but real search returns many editions in provider order (the first
  // Chrono Trigger hit live is a PC port that prices nothing).
  await page.getByRole('link', { name: 'Add', exact: true }).click()
  await page.getByRole('searchbox', { name: /search the catalog/i }).fill('chrono trigger')
  await page.getByRole('button', { name: 'Search', exact: true }).click()
  await page.getByRole('button', { name: /Chrono Trigger on Super Nintendo Entertainment System/ }).first().click()

  await expect(page.getByRole('heading', { name: /your copy of chrono trigger/i })).toBeVisible()
  await page.getByLabel('Edition or variant').fill(stamp)
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
  createdEntryURLs.push(page.url())

  // --- Add a console through hardware search.
  await page.getByRole('link', { name: 'Add', exact: true }).click()
  await page.getByRole('radio', { name: /hardware/i }).check()
  await page.getByRole('searchbox', { name: /search the catalog/i }).fill('gamecube system')
  await page.getByRole('button', { name: 'Search', exact: true }).click()
  // first(): real hardware search also returns regional listings.
  await page.getByRole('button', { name: /Add Gamecube System/ }).first().click()
  await page.getByLabel('Edition or variant').fill(stamp)
  await page.getByLabel('Status').selectOption('shelved')
  await page.getByRole('button', { name: 'Continue' }).click()
  await page.getByRole('button', { name: 'Add to collection' }).click()
  await expect(page.getByRole('heading', { name: 'Gamecube System' })).toBeVisible()
  await expect(page).toHaveURL(/\/entries\//)
  createdEntryURLs.push(page.url())

  // --- Edit granular fields on the console and verify persistence.
  await page.getByLabel('Notes').fill(`${stamp} tested and working`)
  await page.getByLabel('Item condition').selectOption('good')
  await page.getByRole('button', { name: 'Save changes' }).click()
  await page.reload()
  await expect(page.getByLabel('Notes')).toHaveValue(`${stamp} tested and working`)
  await expect(page.getByLabel('Item condition')).toHaveValue('good')

  // --- Custom off-catalog entries (backlog): one for proxy pricing,
  // one as the reorder partner.
  for (const name of [customA, customB]) {
    await page.getByRole('link', { name: 'Add', exact: true }).click()
    await page.getByRole('button', { name: /add an off-catalog item/i }).click()
    await page.getByLabel('Name', { exact: true }).fill(name)
    await page.getByLabel('Platform', { exact: true }).fill('SNES')
    await page.getByRole('button', { name: 'Continue' }).click()
    await page.getByRole('button', { name: 'Continue' }).click() // details defaults: backlog
    await expect(page.getByText(/pricing starts disabled/i)).toBeVisible()
    await page.getByRole('button', { name: 'Add to collection' }).click()
    await expect(page.getByRole('heading', { name })).toBeVisible()
    await expect(page).toHaveURL(/\/entries\//)
    createdEntryURLs.push(page.url())
  }

  // --- Proxy pricing on the custom entry (the page is customB's; go
  // back to customA via its captured URL).
  await page.goto(createdEntryURLs[2])
  // Selecting proxy without a saved target opens the picker rather than
  // flipping the radio (the mode PUT waits for a chosen source), so this
  // is a plain click, not a check that asserts the control toggles.
  await page.getByRole('radio', { name: /proxy/i }).click()
  const picker = page.getByRole('dialog', { name: /choose a price source/i })
  await expect(picker).toBeVisible()
  await picker.getByRole('searchbox', { name: /search the catalog/i }).fill('chrono trigger')
  await picker.getByRole('button', { name: 'Search', exact: true }).click()
  await picker.getByRole('button', { name: /Chrono Trigger on Super Nintendo Entertainment System/ }).first().click()
  await expect(page.getByText('Price source:')).toBeVisible()
  // The proxied listing prices the copy: a dollar value appears
  // (the copy's value and the match breakdown both show one, so take
  // the first rather than assert a single match).
  await expect(page.getByRole('region', { name: 'Pricing' }).getByText(/\$\d/).first()).toBeVisible()

  // --- Pin customA from the collection list.
  await page.getByRole('link', { name: 'Collection', exact: true }).click()
  const rowA = page.getByRole('row', { name: new RegExp(customA) })
  await rowA.getByRole('button', { name: 'Pin' }).click()
  await expect(rowA.getByRole('button', { name: 'Unpin' })).toBeVisible()

  // --- Backlog board: filter to backlog, switch to backlog order,
  // and drag customA below customB with a real pointer gesture. The
  // status filter is a controlled checkbox that rewrites the URL, so a
  // plain click drives it (check() races the re-render); the backlog
  // sort option only exists once the filter is exactly the backlog.
  await page.getByRole('checkbox', { name: 'Backlog' }).click()
  await expect(page.getByRole('checkbox', { name: 'Backlog' })).toBeChecked()
  await page.getByLabel('Sort').selectOption('backlog_rank')
  const board = page.getByRole('region', { name: 'Backlog order' })
  await expect(board).toBeVisible()
  const handleA = board.getByRole('button', { name: `Drag ${customA}` })
  const targetB = board.getByRole('button', { name: `Drag ${customB}` })
  const from = await handleA.boundingBox()
  const to = await targetB.boundingBox()
  if (!from || !to) throw new Error('drag handles not visible')
  await page.mouse.move(from.x + from.width / 2, from.y + from.height / 2)
  await page.mouse.down()
  await page.mouse.move(to.x + to.width / 2, to.y + to.height / 2 + 10, { steps: 12 })
  await page.mouse.up()
  // The write is server-side: reload and the order holds.
  await page.reload()
  // A full reload refetches the list; wait for the board to repaint
  // before reading the order (allTextContents does not auto-wait).
  const reordered = page.getByRole('region', { name: 'Backlog order' })
  await expect(reordered.getByRole('button', { name: `Drag ${customA}` })).toBeVisible()
  const items = reordered.getByRole('listitem')
  const texts = await items.allTextContents()
  const posA = texts.findIndex((t) => t.includes(customA))
  const posB = texts.findIndex((t) => t.includes(customB))
  expect(posA).toBeGreaterThan(posB)

  // --- Saved view round-trip: save this exact state, clear it, restore it.
  acceptNext(page, viewName)
  await page.getByRole('button', { name: 'Save view...' }).click()
  await expect(page.getByRole('combobox', { name: 'Saved view' })).toHaveValue(/./)
  await page.getByRole('button', { name: 'Clear filters' }).click()
  await expect(page.getByRole('region', { name: 'Backlog order' })).toBeHidden()
  await page.getByRole('combobox', { name: 'Saved view' }).selectOption({ label: viewName })
  await expect(page.getByRole('checkbox', { name: 'Backlog' })).toBeChecked()
  await expect(page.getByRole('region', { name: 'Backlog order' })).toBeVisible()

  // --- Dashboard renders every panel.
  await page.getByRole('link', { name: 'Dashboard', exact: true }).click()
  await expect(page.getByRole('region', { name: 'Totals' })).toBeVisible()
  await expect(page.getByRole('region', { name: 'By platform' })).toBeVisible()
  // Fresh products got resolve-time snapshots, so the series exists.
  await expect(page.getByRole('region', { name: 'Collection value over time' })).toBeVisible()
  await expect(page.getByRole('region', { name: 'Recommendations' })).toBeVisible()

  // --- Recommendations: the beaten-and-rated game seeds similar-game
  // candidates from the catalog.
  await page.getByRole('link', { name: 'Recommendations', exact: true }).click()
  await expect(page.getByRole('main', { name: 'Recommendations' })).toBeVisible()
  await expect(page.getByRole('link', { name: /to collection/ }).first()).toBeVisible()

  // --- Cleanup: delete the saved view, then every created entry.
  await page.getByRole('link', { name: 'Collection', exact: true }).click()
  await page.getByRole('combobox', { name: 'Saved view' }).selectOption({ label: viewName })
  acceptNext(page)
  await page.getByRole('button', { name: 'Delete view' }).click()
  await expect(page.getByRole('option', { name: viewName })).toHaveCount(0)
  for (const url of createdEntryURLs) {
    await page.goto(url)
    acceptNext(page)
    await page.getByRole('button', { name: 'Delete entry' }).click()
    await expect(page).toHaveURL(/\/$/)
  }
  await page.goto(`/?item_type=game&item_type=console`)
  await expect(page.getByText(new RegExp(stamp))).toHaveCount(0)
})
