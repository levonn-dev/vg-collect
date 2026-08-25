import { request, type APIRequestContext, type Page } from '@playwright/test'
import { BASE_URL, acceptNext, entryIdFromURL, expect, loginAs, logout, test, type TestUser } from './fixtures'
import { createEntry, deleteEntry, reviewSubmission, setProfile, submitEntry } from './seed'

// Reviewer is always the fixed admin fixture (task e2e grants the role
// up front). candidate-sweep names its product "Chrono Trigger" on
// purpose (see that test); every test deletes what it creates.

// No test uses the worker's shared session (every identity logs in
// explicitly); default storageState would waste an auth hit.
test.use({ storageState: { cookies: [], origins: [] } })

// Gateway budgets /api/auth/* to 240/IP/min (dev); this file mints many
// identities, so it runs serially, keeping the lazily-created admin API
// session (below) warm for every later test and the closing mop.
test.describe.configure({ mode: 'default' })

const stamp = Date.now().toString(36)

// Always SNES platform, so every mint lands on the same search
// identity. Returns id+url (matches seed.createEntry's shape).
async function addCustom(
  page: Page,
  name: string,
  cover?: string,
  region?: string,
  developer?: string,
): Promise<{ id: string; url: string }> {
  await page.getByRole('link', { name: 'Add', exact: true }).click()
  await page.getByRole('button', { name: /custom item/i }).click()
  await page.getByLabel('Name').fill(name)
  await page.getByLabel('Platform').fill('snes')
  await page.getByRole('button', { name: 'Super Nintendo Entertainment System' }).click()
  if (region) await page.getByLabel('Region').selectOption(region)
  if (developer) {
    await page.getByRole('button', { name: 'Add developer' }).click()
    await page.getByLabel('Developers: 1', { exact: true }).fill(developer)
  }
  if (cover) await page.getByLabel(/cover image link/i).fill(cover)
  await page.getByRole('button', { name: 'Continue' }).click()
  await page.getByRole('button', { name: 'Continue' }).click()
  await page.getByRole('button', { name: 'Add to collection' }).click()
  await expect(page).toHaveURL(/\/entries\//)
  const url = page.url()
  return { id: entryIdFromURL(url), url }
}

let adminCtxPromise: Promise<APIRequestContext> | undefined

// Lazily-created, shared admin session for API-only calls; serial test
// order means only the first call logs in.
async function adminApi(): Promise<APIRequestContext> {
  if (!adminCtxPromise) {
    adminCtxPromise = (async () => {
      const ctx = await request.newContext({ baseURL: BASE_URL })
      const res = await ctx.get('/api/auth/login?provider=dev&user=admin')
      if (!res.ok()) throw new Error(`dev login for admin failed: ${res.status()}`)
      return ctx
    })()
  }
  return adminCtxPromise
}

// Arranges a community product via API (skips submit/review UI); fixed
// SNES platform matches every custom entry's pick in this file.
async function approvedCommunityProduct(
  user: TestUser,
  adminCtx: APIRequestContext,
  name: string,
): Promise<{ entryId: string; productId: string }> {
  const { id: entryId } = await createEntry(user.api, { display_name: name })
  const submissionId = await submitEntry(user.api, entryId)
  await reviewSubmission(adminCtx, submissionId, {
    action: 'approve_new',
    product: { type: 'game', name, platform_name: 'Super Nintendo Entertainment System' },
  })
  const res = await user.api.get(`/api/entries/${entryId}`)
  expect(res.ok(), `read entry ${entryId}: ${res.status()}`).toBeTruthy()
  const { product_id } = (await res.json()) as { product_id: string }
  return { entryId, productId: product_id }
}

// Best-effort delete by exact name; logged, not asserted, so a
// residual failure never masks the journey.
async function deleteCommunityProduct(ctx: APIRequestContext, name: string) {
  const lane = await ctx.get(`/api/search?type=game&q=${encodeURIComponent(name)}`)
  if (!lane.ok()) return
  const body = (await lane.json()) as { results?: { product_id?: string; name: string; origin?: string }[] }
  for (const p of body.results ?? []) {
    if (p.origin === 'community' && p.name === name && p.product_id) {
      const del = await ctx.delete(`/api/admin/products/${p.product_id}`)
      console.log(`teardown: product ${p.product_id} "${name}" -> ${del.status()}`)
    }
  }
}

// Removes a stray "Chrono Trigger" left by a crashed run, in whichever
// state it was left (community lane or unmatched worklist); the three
// baseline provider-matched copies appear in neither list and 409 on
// delete anyway, so they're untouched. Empty/404 is the normal case.
async function deleteResidualChronoTriggerProduct(ctx: APIRequestContext) {
  const ids = new Map<string, string>()
  const lane = await ctx.get('/api/search?type=game&q=chrono%20trigger')
  if (lane.ok()) {
    const body = (await lane.json()) as { results?: { product_id?: string; name: string; origin?: string }[] }
    for (const p of body.results ?? []) {
      if (p.origin === 'community' && p.name === 'Chrono Trigger' && p.product_id) ids.set(p.product_id, p.name)
    }
  }
  const unmatched = await ctx.get('/api/admin/products/unmatched?offset=0')
  if (unmatched.ok()) {
    const body = (await unmatched.json()) as { products?: { id: string; name: string }[] }
    for (const p of body.products ?? []) if (p.name === 'Chrono Trigger') ids.set(p.id, p.name)
  }
  for (const [id, name] of ids) {
    const del = await ctx.delete(`/api/admin/products/${id}`)
    console.log(`teardown: product ${id} "${name}" -> ${del.status()}`)
  }
}

test.afterAll(async () => {
  test.setTimeout(60_000)
  try {
    await deleteResidualChronoTriggerProduct(await adminApi())
  } catch (err) {
    console.log('teardown: product mop skipped (best-effort):', err)
  } finally {
    if (adminCtxPromise) await (await adminCtxPromise).dispose()
  }
})

test('reject round trip: submit, admin rejects, submitter reads the reason', async ({ page, browser, freshUser }) => {
  test.setTimeout(60_000)
  const submitter = await freshUser()
  const name = `e2e Reject Cart ${stamp}`
  const { url } = await createEntry(submitter.api, { display_name: name })

  await loginAs(page, submitter.name)
  await page.goto(url)
  await page.getByRole('button', { name: 'Submit to catalog' }).click()
  await expect(page.getByText(/waiting for review/i)).toBeVisible()

  // Separate context for admin keeps the submitter's page logged in;
  // saves a re-login.
  const adminContext = await browser.newContext({ baseURL: BASE_URL })
  const adminPage = await adminContext.newPage()
  await loginAs(adminPage, 'admin')
  await adminPage.getByRole('link', { name: 'Admin', exact: true }).click()
  await adminPage.getByRole('tab', { name: 'Submissions' }).click()
  const row = adminPage.locator('tbody tr').filter({ hasText: name })
  await row.getByRole('button', { name: 'Review' }).click()
  const panel = adminPage.locator(`[aria-label="Review ${name}"]`)
  await panel.getByLabel('Rejection reason').fill('not a shared item')
  await panel.getByRole('button', { name: 'Reject' }).click()
  await expect(adminPage.locator('tbody tr').filter({ hasText: name })).toHaveCount(0)
  await adminContext.close()

  await page.goto(url)
  await expect(page.getByText(/rejected: not a shared item/i)).toBeVisible()
  await expect(page.getByRole('button', { name: 'Resubmit to catalog' })).toBeVisible()
})

test('approve as new adopts the entry silently', async ({ page, browser, freshUser }) => {
  test.setTimeout(60_000)
  const submitter = await freshUser()
  const name = `e2e Approve Cart ${stamp}`
  const cover = 'https://img.example/ct.jpg'

  await loginAs(page, submitter.name)
  const { id: entryId, url: entryURL } = await addCustom(page, name, cover)
  await page.getByRole('button', { name: 'Submit to catalog' }).click()
  await expect(page.getByText(/waiting for review/i)).toBeVisible()

  // Separate context for admin (see reject test above).
  const adminContext = await browser.newContext({ baseURL: BASE_URL })
  const adminPage = await adminContext.newPage()
  await loginAs(adminPage, 'admin')
  await adminPage.getByRole('link', { name: 'Admin', exact: true }).click()
  await adminPage.getByRole('tab', { name: 'Submissions' }).click()
  // Scoped to the queue region: approve-new also mints a same-named
  // community row below it, which an unscoped locator would double-count.
  const queue = adminPage.getByRole('region', { name: 'Catalog submissions' })
  await queue.locator('tbody tr').filter({ hasText: name }).first().getByRole('button', { name: 'Review' }).click()
  const review = adminPage.locator(`[aria-label="Review ${name}"]`)
  await expect(review.getByLabel('Name')).toHaveValue(name)
  await review.getByRole('button', { name: 'Approve as new product' }).click()
  await expect(queue.locator('tbody tr').filter({ hasText: name })).toHaveCount(0)

  // Same-URL reload keeps the router's justAdded state ("Added to your
  // collection." is a separate status element), so the approval check
  // below is scoped to its own text, not the bare role.
  await page.goto(entryURL)
  await expect(page.getByText(/custom item/)).toHaveCount(0)
  await expect(page.getByRole('button', { name: 'Submit to catalog' })).toHaveCount(0)
  // The approval banner shows on the silently-adopted entry.
  await expect(page.getByRole('status').filter({ hasText: /approved/i })).toBeVisible()
  // Attribute check, not visibility: img.example never resolves and
  // there's no onError, so the <img> stays attached regardless.
  await expect(page.getByRole('main', { name: 'Entry detail' }).locator('img')).toHaveAttribute('src', cover)
  // Dismiss is server-stamped; wait for the ack POST before reload so
  // reload can't cancel it in flight.
  const ackResponse = page.waitForResponse((r) => r.url().includes('/submission/ack'))
  await page.getByRole('button', { name: 'Dismiss approval notice' }).click()
  await ackResponse
  await page.reload()
  await expect(page.getByRole('button', { name: 'Dismiss approval notice' })).toHaveCount(0)

  // Entry before product: teardown deletes the account too late, and
  // a referenced product 409s.
  await deleteEntry(submitter.api, entryId)
  await deleteCommunityProduct(adminContext.request, name)
  await adminContext.close()
})

test('community lane adopt: a search finds and adds the approved product', async ({ page, freshUser }) => {
  test.setTimeout(60_000)
  const user = await freshUser()
  // Name avoids the substring "community": the row's own origin badge
  // reads that word.
  const name = `e2e Adopt Cart ${stamp}`
  const { entryId: arrangeEntryId } = await approvedCommunityProduct(user, await adminApi(), name)

  await loginAs(page, user.name)
  await page.getByRole('link', { name: 'Add', exact: true }).click()
  await page.getByRole('searchbox', { name: /search for games and hardware/i }).fill(name)
  await page.getByRole('button', { name: 'Search', exact: true }).click()
  await expect(page.getByText('community')).toBeVisible()
  // Scoped to the community-tagged row to disambiguate among other results.
  const communityResult = page.getByRole('region', { name: 'Search' }).locator('li').filter({ hasText: 'community' })
  await communityResult.getByRole('button', { name: `${name} on Super Nintendo Entertainment System` }).click()
  await page.getByRole('button', { name: 'Continue' }).click()
  await page.getByRole('button', { name: 'Add to collection' }).click()
  await expect(page).toHaveURL(/\/entries\//)
  const adoptedEntryId = entryIdFromURL(page.url())

  // Entries before product: teardown too late, referenced product 409s.
  await deleteEntry(user.api, arrangeEntryId)
  await deleteEntry(user.api, adoptedEntryId)
  await deleteCommunityProduct(await adminApi(), name)
})

test('candidate sweep flags a colliding community product, and promote clears it', async ({ page, freshUser }) => {
  test.setTimeout(150_000)
  // Named to collide with IGDB's real Chrono Trigger on SNES, which is
  // exactly what candidate sweep looks for. Deletes everything it
  // creates; afterAll above is only the crash-safety net.
  const submitter = await freshUser()
  const adopter = await freshUser()
  const { entryId: submitterEntryId, productId } = await approvedCommunityProduct(submitter, await adminApi(), 'Chrono Trigger')
  // Mirrors ConfirmStep.tsx's community branch: skips resolve, creates
  // straight off the product id.
  const { id: adopterEntryId } = await createEntry(adopter.api, { product_id: productId })

  await loginAs(page, 'admin')
  await page.getByRole('link', { name: 'Admin', exact: true }).click()
  await page.getByRole('button', { name: 'Trigger catalog refresh' }).click()
  await expect(page.getByText('Refresh started.')).toBeVisible()
  const candidates = page.getByRole('region', { name: 'Promote candidates' })
  const candRow = candidates.locator('tbody tr').filter({ hasText: 'Chrono Trigger' })
  // Refresh is detached (202); candidate sweep is its last phase,
  // landing seconds later. Admin queries have a 5min staleTime, so only
  // a full reload forces a fresh read; poll on reload until flagged.
  await expect(async () => {
    await page.reload()
    await expect(candRow.first()).toBeVisible({ timeout: 5_000 })
  }).toPass({ timeout: 90_000 })
  await candRow.first().getByRole('button', { name: 'Review' }).click()
  const promote = page.locator('[aria-label="Promote Chrono Trigger"]')
  await promote.getByRole('button', { name: 'Promote to provider identity' }).click()
  await promote.getByRole('button', { name: 'Search', exact: true }).click()
  acceptNext(page)
  await promote.getByRole('button', { name: /Chrono Trigger on Super Nintendo Entertainment System/ }).first().click()
  await expect(candRow).toHaveCount(0, { timeout: 30_000 })

  // Entries before product: a referenced product refuses to delete (409).
  await deleteEntry(submitter.api, submitterEntryId)
  await deleteEntry(adopter.api, adopterEntryId)

  const worklist = page.getByRole('region', { name: 'Unmatched products' })
  const promotedRow = worklist.locator('tbody tr').filter({ hasText: 'Chrono Trigger' })
  await expect(promotedRow.first()).toBeVisible()
  await promotedRow.first().getByRole('button', { name: 'Fix mapping' }).click()
  acceptNext(page)
  await worklist.getByRole('button', { name: 'Delete' }).click()
  await expect(worklist.locator('tbody tr').filter({ hasText: 'Chrono Trigger' })).toHaveCount(0)
})

test('region flows through queue, review, and the next add wizard', async ({ page, freshUser }) => {
  test.setTimeout(90_000)
  const name = `e2e Region Cart ${stamp}`
  const submitter = await freshUser()
  // Non-private profile needed for the queue's submitter link
  // (SubmitterCell falls back to text otherwise); unlisted is enough.
  await setProfile(submitter.api, { profile_visibility: 'unlisted' })

  await loginAs(page, submitter.name)
  const { id: entryId } = await addCustom(page, name, undefined, 'ntsc_j', 'Garage Team')
  // The custom credit fact renders on the entry itself right away.
  await expect(page.getByText('Developed by Garage Team')).toBeVisible()
  await page.getByRole('button', { name: 'Submit to catalog' }).click()
  await expect(page.getByText(/waiting for review/i)).toBeVisible()

  // Queue row shows the region and a live submitter-handle link; review
  // panel prefills region.
  await logout(page)
  await loginAs(page, 'admin')
  await page.getByRole('link', { name: 'Admin', exact: true }).click()
  await page.getByRole('tab', { name: 'Submissions' }).click()
  // Scoped to the queue region (same reason as the approve test above).
  const queue = page.getByRole('region', { name: 'Catalog submissions' })
  const row = queue.locator('tbody tr').filter({ hasText: name })
  await expect(row).toContainText('NTSC-J')
  await expect(row.getByRole('link')).toHaveAttribute('href', /^\/u\//)
  await row.getByRole('button', { name: 'Review' }).click()
  const panel = page.locator(`[aria-label="Review ${name}"]`)
  await expect(panel.getByLabel('Region')).toHaveValue('ntsc_j')
  await expect(panel.getByLabel('Developers: 1', { exact: true })).toHaveValue('Garage Team')
  await panel.getByRole('button', { name: 'Approve as new product' }).click()
  await expect(queue.locator('tbody tr').filter({ hasText: name })).toHaveCount(0)

  // New viewer finds the product; region tag and the wizard's own
  // default both read back the approved value (defaultDetails seeds off
  // the community pick). Stops here, no submit needed.
  const viewer = await freshUser()
  await logout(page)
  await loginAs(page, viewer.name)
  await page.getByRole('link', { name: 'Add', exact: true }).click()
  await page.getByRole('searchbox', { name: /search for games and hardware/i }).fill(name)
  await page.getByRole('button', { name: 'Search', exact: true }).click()
  const communityResult = page.getByRole('region', { name: 'Search' }).locator('li').filter({ hasText: 'community' })
  await expect(communityResult.getByText('NTSC-J')).toBeVisible()
  await communityResult.getByRole('button', { name: `${name} on Super Nintendo Entertainment System` }).click()
  await expect(page.getByLabel('Region')).toHaveValue('ntsc_j')

  // Entry before product: teardown too late, referenced product 409s.
  // This leg's own product needs its own delete.
  await deleteEntry(submitter.api, entryId)
  await deleteCommunityProduct(await adminApi(), name)
})

test('admin deletes a community product from the cleanup list', async ({ page, freshUser }) => {
  test.setTimeout(60_000)
  const submitter = await freshUser()
  const name = `e2e Cleanup Cart ${stamp}`
  const { entryId } = await approvedCommunityProduct(submitter, await adminApi(), name)
  // Entry deleted first so the product is unreferenced; otherwise
  // delete below 409s instead of the row disappearing.
  await deleteEntry(submitter.api, entryId)

  await loginAs(page, 'admin')
  await page.getByRole('link', { name: 'Admin', exact: true }).click()
  await page.getByRole('tab', { name: 'Submissions' }).click()
  const list = page.getByRole('region', { name: 'Community products' })
  const row = list.locator('tbody tr').filter({ hasText: name })
  const loadMore = list.getByRole('button', { name: 'Load more' })
  // List sorts oldest-updated first; poll Load more until the row
  // surfaces or the button disappears.
  await expect(async () => {
    if ((await row.count()) === 0 && (await loadMore.count()) > 0) await loadMore.click()
    await expect(row).toBeVisible({ timeout: 2_000 })
  }).toPass({ timeout: 30_000 })
  // Same confirm question as every other destructive action.
  acceptNext(page)
  await row.getByRole('button', { name: 'Delete' }).click()
  await expect(row).toHaveCount(0)
})
