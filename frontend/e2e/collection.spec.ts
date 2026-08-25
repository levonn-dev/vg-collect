import { acceptNext, expect, loginAs, test } from './fixtures'
import { createEntry, createTag, deleteEntry, deleteTag } from './seed'

// Six tests run on the shared worker user, asserting only their own
// stamped entries (bulk edit's tag-filtered count is isolated by the
// stamped tag); the facet test uses freshUser since facets aggregate.
const stamp = `e2e-col-${Date.now()}`

test('granular field edits persist', async ({ page, api }) => {
  await page.goto('/')
  const entry = await createEntry(api, { display_name: `Edit Target ${stamp}`, item_type: 'console' })

  await page.goto(entry.url)
  await page.getByLabel('Notes').fill(`${stamp} tested and working`)
  await page.getByLabel('Item condition').selectOption('good')
  await page.getByRole('button', { name: 'Save changes' }).click()
  // Wait for save confirmation before reload to avoid reading back stale values.
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
  // Controlled checkbox rewrites the URL; click, not check() (races the
  // re-render). Backlog sort exists only once filtered.
  await page.getByRole('checkbox', { name: 'Backlog' }).click()
  await expect(page.getByRole('checkbox', { name: 'Backlog' })).toBeChecked()
  await page.getByLabel('Sort').selectOption('backlog_rank')
  const board = page.getByRole('region', { name: 'Backlog order' })
  await expect(board).toBeVisible()
  const handleA = board.getByRole('button', { name: `Drag ${customA}` })
  // A starts above B (creation order); post-drag check proves a real move.
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

  // Scroll target into view first: an extra backlog row can push it
  // past the viewport fold, leaving a clipped drop as a no-op.
  const rowBLoc = board.getByRole('listitem').filter({ hasText: customB })
  await rowBLoc.scrollIntoViewIfNeeded()
  const from = await handleA.boundingBox()
  const rowB = await rowBLoc.boundingBox()
  if (!from || !rowB) throw new Error('drag handles not visible')
  // closestCenter picks the nearest droppable by row center; stay in
  // the handle column (horizontal move -> over==null). Nudge first to
  // arm PointerSensor.
  const dragX = from.x + from.width / 2
  await page.mouse.move(dragX, from.y + from.height / 2)
  await page.mouse.down()
  await page.mouse.move(dragX, from.y + from.height / 2 + 8)
  await page.mouse.move(dragX, rowB.y + rowB.height - 4, { steps: 20 })
  await page.mouse.move(dragX, rowB.y + rowB.height - 4)
  await page.mouse.up()

  // Write is server-side; reload refetches, so wait for the board to
  // repaint before reading order.
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
  // Close the panel first: reapplying a shelf never auto-opens it
  // (count badge is the only signal).
  await page.getByRole('button', { name: /^Filters/ }).click()

  acceptNext(page, viewName)
  await page.getByRole('button', { name: 'Save shelf...' }).click()
  await expect(page.getByRole('combobox', { name: 'Shelf' })).toHaveValue(/./)
  await page.getByRole('button', { name: 'Clear filters' }).click()
  await expect(page.getByRole('region', { name: 'Backlog order' })).toBeHidden()
  await page.getByRole('combobox', { name: 'Shelf' }).selectOption({ label: viewName })
  // Reapply restores filters but doesn't auto-open the panel; open it
  // before the checkbox is reachable.
  await page.getByRole('button', { name: /^Filters/ }).click()
  await expect(page.getByRole('checkbox', { name: 'Backlog' })).toBeChecked()
  await expect(page.getByRole('region', { name: 'Backlog order' })).toBeVisible()

  // No helper deletes a view over the API; delete through the UI.
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
  // Tags list was fetched before this tag existed and never refetches;
  // reload to pick it up.
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
  // Mirrors CustomConfirm's platform-picker payload: platform_name +
  // platform_igdb_id (19 = SNES in fixture data).
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

  // Filters combine with AND (filterWhere, store_entries.go); clear
  // developer first so publisher's assertions prove it alone.
  await page.getByRole('button', { name: 'Clear filters' }).click()

  // Facets are user-global aggregates (fetchEntryFacets); publisher
  // group renders the same way developer did above.
  const publisherGroup = page.getByRole('group', { name: 'Publisher' })
  await expect(publisherGroup.getByText(pubName)).toBeVisible()
  await publisherGroup.getByRole('checkbox', { name: pubName }).click()
  await expect(page.getByRole('row', { name: new RegExp(creditedName) })).toBeVisible()
  await expect(page.getByRole('row', { name: new RegExp(uncreditedName) })).toHaveCount(0)

  await deleteEntry(fresh.api, credited.id)
  await deleteEntry(fresh.api, uncredited.id)
})
