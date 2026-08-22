import { acceptNext, expect, loginAs, test } from './fixtures'
import { createEntry, createTag, deleteEntry, deleteTag } from './seed'

// Seven independent tests covering collection management: granular
// field edits, pinning, backlog reordering, shelves (saved filter
// views), bulk edit, and the platform/developer/publisher facets. Six
// run on the shared worker user and assert only against their own
// stamped entries; the bulk edit test's tag-filtered row count is the
// one count assertion in the file, safe because the stamped tag
// isolates exactly its own two rows. The developer/publisher facet
// test uses a fresh user instead, since those facets aggregate across
// every entry the signed-in user owns.
const stamp = `e2e-col-${Date.now()}`

test('granular field edits persist', async ({ page, api }) => {
  await page.goto('/')
  const entry = await createEntry(api, { display_name: `Edit Target ${stamp}`, item_type: 'console' })

  await page.goto(entry.url)
  await page.getByLabel('Notes').fill(`${stamp} tested and working`)
  await page.getByLabel('Item condition').selectOption('good')
  await page.getByRole('button', { name: 'Save changes' }).click()
  // Wait for the save confirmation before reloading: reloading right
  // after the click can race the write and read back stale values.
  await expect(page.getByText('Saved.')).toBeVisible()
  await page.reload()
  await expect(page.getByLabel('Notes')).toHaveValue(`${stamp} tested and working`)
  await expect(page.getByLabel('Item condition')).toHaveValue('good')

  await deleteEntry(api, entry.id)
})

test('pin from the list', async ({ page, api }) => {
  await page.goto('/')
  const name = `Pin Target ${stamp}`
  const entry = await createEntry(api, { display_name: name })

  await page.getByRole('link', { name: 'Collection', exact: true }).click()
  const row = page.getByRole('row', { name: new RegExp(name) })
  await row.getByRole('button', { name: 'Pin' }).click()
  await expect(row.getByRole('button', { name: 'Unpin' })).toBeVisible()

  await deleteEntry(api, entry.id)
})

test('backlog reorder persists server-side', async ({ page, api }) => {
  test.setTimeout(60_000)
  await page.goto('/')
  const customA = `Repro Alpha ${stamp}`
  const customB = `Repro Beta ${stamp}`
  const entryA = await createEntry(api, { display_name: customA, status: 'backlog' })
  const entryB = await createEntry(api, { display_name: customB, status: 'backlog' })

  await page.getByRole('link', { name: 'Collection', exact: true }).click()
  // The chip is behind the Filters disclosure; open it first.
  await page.getByRole('button', { name: /^Filters/ }).click()
  // The status filter is a controlled checkbox that rewrites the URL,
  // so a plain click drives it (check() races the re-render); the
  // backlog sort option only exists once the filter is exactly the
  // backlog.
  await page.getByRole('checkbox', { name: 'Backlog' }).click()
  await expect(page.getByRole('checkbox', { name: 'Backlog' })).toBeChecked()
  await page.getByLabel('Sort').selectOption('backlog_rank')
  const board = page.getByRole('region', { name: 'Backlog order' })
  await expect(board).toBeVisible()
  const handleA = board.getByRole('button', { name: `Drag ${customA}` })
  // Pre-drag order: A is above B (backlog rank appends in creation
  // order), so the post-drag check below reflects a real move and not
  // the starting layout.
  await expect(handleA).toBeVisible()
  const orderOf = async () => {
    const texts = await board.getByRole('listitem').allTextContents()
    return { a: texts.findIndex((t) => t.includes(customA)), b: texts.findIndex((t) => t.includes(customB)) }
  }
  await expect(async () => {
    const { a, b } = await orderOf()
    expect(a).toBeGreaterThanOrEqual(0)
    expect(b).toBeGreaterThanOrEqual(0)
    expect(a).toBeLessThan(b)
  }).toPass()

  // Scroll targetB into view before measuring: an extra backlog row
  // (another entry can already sit in the backlog on a long-lived
  // stack) can push the last row to the viewport fold, and a drop
  // aimed past a clipped row lands outside the viewport where the
  // pointer never lands on it, leaving over==null and a no-op drop.
  const rowBLoc = board.getByRole('listitem').filter({ hasText: customB })
  await rowBLoc.scrollIntoViewIfNeeded()
  const from = await handleA.boundingBox()
  const rowB = await rowBLoc.boundingBox()
  if (!from || !rowB) throw new Error('drag handles not visible')
  // Drag handles are left-aligned and closestCenter picks the
  // droppable nearest the dragged row's center, so stay in the handle
  // column (a horizontal excursion shoves the row out of the list ->
  // over==null) and sink past targetB's midpoint. Nudge first to arm
  // the PointerSensor, step down the column to targetB's lower edge,
  // then settle before release.
  const dragX = from.x + from.width / 2
  await page.mouse.move(dragX, from.y + from.height / 2)
  await page.mouse.down()
  await page.mouse.move(dragX, from.y + from.height / 2 + 8)
  await page.mouse.move(dragX, rowB.y + rowB.height - 4, { steps: 20 })
  await page.mouse.move(dragX, rowB.y + rowB.height - 4)
  await page.mouse.up()

  // The write is server-side: reload and the order holds. A full
  // reload refetches the list, so wait for the board to repaint
  // before reading the order.
  await page.reload()
  await expect(board.getByRole('button', { name: `Drag ${customA}` })).toBeVisible()
  await expect(async () => {
    const { a, b } = await orderOf()
    expect(a).toBeGreaterThanOrEqual(0)
    expect(b).toBeGreaterThanOrEqual(0)
    expect(a).toBeGreaterThan(b)
  }).toPass()

  await deleteEntry(api, entryA.id)
  await deleteEntry(api, entryB.id)
})

test('shelf round-trip: save, clear, reapply', async ({ page, api }) => {
  await page.goto('/')
  const customA = `Repro Alpha Shelf ${stamp}`
  const customB = `Repro Beta Shelf ${stamp}`
  const entryA = await createEntry(api, { display_name: customA, status: 'backlog' })
  const entryB = await createEntry(api, { display_name: customB, status: 'backlog' })
  const viewName = `View ${stamp}`

  await page.getByRole('link', { name: 'Collection', exact: true }).click()
  await page.getByRole('button', { name: /^Filters/ }).click()
  await page.getByRole('checkbox', { name: 'Backlog' }).click()
  await expect(page.getByRole('checkbox', { name: 'Backlog' })).toBeChecked()
  await page.getByLabel('Sort').selectOption('backlog_rank')
  await expect(page.getByRole('region', { name: 'Backlog order' })).toBeVisible()
  // Close the panel before the round trip below: reapplying a shelf
  // never auto-opens it (the Filters count badge is the only signal),
  // so the panel must already be closed for the later re-open click to
  // actually prove that instead of just toggling it shut.
  await page.getByRole('button', { name: /^Filters/ }).click()

  acceptNext(page, viewName)
  await page.getByRole('button', { name: 'Save shelf...' }).click()
  await expect(page.getByRole('combobox', { name: 'Shelf' })).toHaveValue(/./)
  await page.getByRole('button', { name: 'Clear filters' }).click()
  await expect(page.getByRole('region', { name: 'Backlog order' })).toBeHidden()
  await page.getByRole('combobox', { name: 'Shelf' }).selectOption({ label: viewName })
  // Reapplying the shelf restores its filters but never auto-opens the
  // panel (the Filters count badge is the signal, not a forced
  // reveal); open it again before the checkbox is reachable.
  await page.getByRole('button', { name: /^Filters/ }).click()
  await expect(page.getByRole('checkbox', { name: 'Backlog' })).toBeChecked()
  await expect(page.getByRole('region', { name: 'Backlog order' })).toBeVisible()

  // Cleanup: delete the shelf through the UI (no helper deletes a view
  // over the API), then both entries.
  acceptNext(page)
  await page.getByRole('button', { name: 'Delete shelf' }).click()
  await expect(page.getByRole('option', { name: viewName })).toHaveCount(0)
  await deleteEntry(api, entryA.id)
  await deleteEntry(api, entryB.id)
})

test('bulk edit applies tags and status; the tag facet isolates rows', async ({ page, api }) => {
  await page.goto('/')
  const customA = `Bulk Alpha ${stamp}`
  const customB = `Bulk Beta ${stamp}`
  const entryA = await createEntry(api, { display_name: customA })
  const entryB = await createEntry(api, { display_name: customB })

  await page.getByRole('link', { name: 'Collection', exact: true }).click()

  const bulkTagName = `bulk ${stamp}`
  const bulkTagId = await createTag(api, bulkTagName)
  // The page's tags list was fetched before this tag existed and never
  // refetches on its own; reload to pick it up before the bulk bar's
  // Add tags group can offer it.
  await page.reload()

  await page.getByRole('button', { name: 'Bulk edit' }).click()
  await page.getByRole('checkbox', { name: `Select ${customA}` }).click()
  await page.getByRole('checkbox', { name: `Select ${customB}` }).click()
  const bulkBar = page.getByRole('region', { name: 'Bulk edit' })
  await bulkBar.getByRole('group', { name: 'Add tags' }).getByRole('checkbox', { name: bulkTagName }).click()
  await bulkBar.getByLabel('Status').selectOption('shelved')
  await bulkBar.getByRole('button', { name: 'Apply' }).click()
  await expect(page.getByRole('status')).toHaveText('Updated 2 entries.')

  // Filter down to exactly the two custom rows via the new tag.
  await page.getByRole('button', { name: /^Filters/ }).click()
  await page.getByRole('checkbox', { name: bulkTagName }).click()
  await expect(page.getByRole('row', { name: new RegExp(customA) })).toBeVisible()
  await expect(page.getByRole('row', { name: new RegExp(customB) })).toBeVisible()
  await expect(page.getByRole('table').getByRole('row')).toHaveCount(3) // header + the two custom rows
  await expect(page.getByRole('link', { name: 'Chrono Trigger' })).toHaveCount(0)
  await page.getByRole('button', { name: 'Clear filters' }).click()

  await deleteTag(api, bulkTagId)
  await deleteEntry(api, entryA.id)
  await deleteEntry(api, entryB.id)
})

test('platform facet from a picker-created entry', async ({ page, api }) => {
  await page.goto('/')
  const name = `Picker Cart ${stamp}`
  // Mirrors the payload the custom-add wizard sends once a platform
  // picker suggestion is chosen (CustomConfirm in AddWizard.tsx):
  // platform_name plus the picker's platform_igdb_id, here Super
  // Nintendo Entertainment System (igdb platform id 19 in the catalog
  // fixture data).
  const entry = await createEntry(api, {
    display_name: name,
    platform_name: 'Super Nintendo Entertainment System',
    platform_igdb_id: 19,
  })

  await page.goto('/collection')
  // The filter panel starts collapsed; the facet chips render only once opened.
  await page.getByRole('button', { name: /^Filters/ }).click()
  // The SNES facet exists because the entry carries platform_igdb_id.
  const filter = page.getByRole('group', { name: /platform/i })
  await expect(filter.getByText('Super Nintendo Entertainment System')).toBeVisible()

  await deleteEntry(api, entry.id)
})

test('developer and publisher facets filter the collection', async ({ page, freshUser }) => {
  const fresh = await freshUser()
  const devName = `Garage Team ${stamp}`
  const pubName = `Garage Label ${stamp}`
  const creditedName = `Credited Cart ${stamp}`
  const uncreditedName = `Uncredited Cart ${stamp}`
  const credited = await createEntry(fresh.api, {
    display_name: creditedName,
    developers: [devName],
    publishers: [pubName],
  })
  const uncredited = await createEntry(fresh.api, { display_name: uncreditedName })

  await loginAs(page, fresh.name)
  await page.getByRole('link', { name: 'Collection', exact: true }).click()
  await page.getByRole('button', { name: /^Filters/ }).click()

  const developerGroup = page.getByRole('group', { name: 'Developer' })
  await expect(developerGroup.getByText(devName)).toBeVisible()
  await developerGroup.getByRole('checkbox', { name: devName }).click()
  await expect(page.getByRole('row', { name: new RegExp(creditedName) })).toBeVisible()
  await expect(page.getByRole('row', { name: new RegExp(uncreditedName) })).toHaveCount(0)

  // Clear the developer filter before checking publisher: every filter
  // dimension combines with AND (filterWhere in the collection
  // service's store_entries.go), so leaving developer checked would
  // make the publisher assertions below pass on the AND of both rather
  // than proving the publisher facet filters on its own.
  await page.getByRole('button', { name: 'Clear filters' }).click()

  // Facets are user-global aggregates (fetchEntryFacets sweeps every
  // entry this user owns), so the publisher group renders from the
  // credited entry's own seeded value the same way developers did above.
  const publisherGroup = page.getByRole('group', { name: 'Publisher' })
  await expect(publisherGroup.getByText(pubName)).toBeVisible()
  await publisherGroup.getByRole('checkbox', { name: pubName }).click()
  await expect(page.getByRole('row', { name: new RegExp(creditedName) })).toBeVisible()
  await expect(page.getByRole('row', { name: new RegExp(uncreditedName) })).toHaveCount(0)

  await deleteEntry(fresh.api, credited.id)
  await deleteEntry(fresh.api, uncredited.id)
})
