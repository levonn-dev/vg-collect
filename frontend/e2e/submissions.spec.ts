import { request, type APIRequestContext, type Page } from '@playwright/test'
import { BASE_URL, acceptNext, entryIdFromURL, expect, loginAs, logout, test, type TestUser } from './fixtures'
import { createEntry, deleteEntry, reviewSubmission, setProfile, submitEntry } from './seed'

// Catalog-submissions journey across six tests: a submitter proposes an
// item, an admin reviews it, and - once approved - it becomes a shared
// community product other collectors can find and adopt. Every
// submitter and adopter below is a throwaway freshUser; the reviewer is
// always the fixed admin fixture (task e2e grants the role up front).
// The candidate-sweep test names its product "Chrono Trigger" on
// purpose - see that test's own comment - and every test deletes
// everything it creates, so the stack stays re-runnable.

// No test here touches the worker's own shared session (every identity
// below is either a freshUser or the admin fixture, and both log in
// explicitly), so the default storageState would only spend an auth hit
// standing up a session nothing reads.
test.use({ storageState: { cookies: [], origins: [] } })

// The gateway budgets /api/auth/* per IP per minute (120 on this dev
// stack; production keeps it far tighter). This file mints or logs in
// more identities than most - a submitter, an admin review, and (in
// three tests) a second collector - so it runs its own tests in
// declared order on one worker rather than Playwright's default full
// parallelism: that is what makes the lazily-created admin API session
// below reliably warm by the time any later test or the closing mop
// reaches for it (one login total instead of one per test).
test.describe.configure({ mode: 'default' })

const stamp = Date.now().toString(36)

// addCustom drives the custom-item add wizard end to end, always on the
// Super Nintendo Entertainment System platform so every test in this
// file that mints one lands on the same search identity. region and
// developer are optional trailing params rather than an options object
// so the plain two-arg call sites below need no changes. Returns the id
// alongside the url (matching seed.createEntry's own shape) so callers
// can delete the entry over the API without re-parsing the page.
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
    await page.getByLabel('Developers 1', { exact: true }).fill(developer)
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

// The reviewer is the fixed admin fixture, not a per-test mint - one
// shared, lazily-created session serves every arrange or teardown call
// below that only needs the admin API, not the admin page. Tests run in
// declared order on one worker (see test.describe.configure above), so
// the first call here pays the one real login; every later call,
// including the crash-safety mop in afterAll, reuses it for free.
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

// approvedCommunityProduct arranges a ready-made community product over
// the API, skipping the submit-and-review UI for tests where neither
// surface is the subject: a throwaway submitter creates and submits a
// custom entry, the admin verdict mints the product on a fixed platform
// (Super Nintendo Entertainment System, matching every custom entry's
// own platform pick in this file, so search results stay consistent
// across tests), and the submitter's own entry - silently product-
// backed now, per the approve test's own proof elsewhere in this file -
// hands back the resulting product id.
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

// deleteCommunityProduct (admin session) removes a still-community
// product by its exact name. Best-effort: logged, not asserted, so a
// residual delete failure never masks the journey itself.
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

// deleteResidualChronoTriggerProduct (admin session) removes the
// "Chrono Trigger" product the candidate-sweep test mints, in whichever
// state a crashed run left it: still community (surfaces in the search
// community lane) or already promoted (surfaces in the unmatched
// worklist) - a crashed run's stray copy would collide with the next
// run's own mint. The three baseline matched "Chrono Trigger" products
// are provider-identified with a pricecharting mapping, so they appear
// in neither list and are never touched; the admin delete refuses
// matched or still-referenced products anyway (409), best-effort
// treated as success. Empty lists and 404s are success too - a green
// run already cleaned up after itself, so this usually finds nothing.
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

  // A second, independent context carries the admin session alongside
  // the submitter's page instead of overwriting it - the submitter's
  // page needs no re-login to read the result below, saving an auth hit.
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

  // A second, independent context carries the admin session alongside
  // the submitter's page instead of overwriting it - see the reject
  // test above for why.
  const adminContext = await browser.newContext({ baseURL: BASE_URL })
  const adminPage = await adminContext.newPage()
  await loginAs(adminPage, 'admin')
  await adminPage.getByRole('link', { name: 'Admin', exact: true }).click()
  await adminPage.getByRole('tab', { name: 'Submissions' }).click()
  // Scoped to the queue's own region: the Submissions tab also renders
  // the community products cleanup list right below it, and approve-new
  // mints this same-named product as a community row there, so an
  // unscoped page-wide tbody-row locator would double-count once that
  // second table refetches after the verdict.
  const queue = adminPage.getByRole('region', { name: 'Catalog submissions' })
  await queue.locator('tbody tr').filter({ hasText: name }).first().getByRole('button', { name: 'Review' }).click()
  const review = adminPage.locator(`[aria-label="Review ${name}"]`)
  await expect(review.getByLabel('Name')).toHaveValue(name)
  await review.getByRole('button', { name: 'Approve as new product' }).click()
  await expect(queue.locator('tbody tr').filter({ hasText: name })).toHaveCount(0)

  // The entry is product-backed now (the block is custom-only). The
  // page never left this entry since the wizard landed here, so the
  // reload below is a same-URL reload rather than a fresh navigation
  // and keeps the router's own justAdded state - its "Added to your
  // collection." banner is a second, unrelated role="status" element,
  // so the approval check below is scoped to its own text rather than
  // the bare role.
  await page.goto(entryURL)
  await expect(page.getByText(/custom item/)).toHaveCount(0)
  await expect(page.getByRole('button', { name: 'Submit to catalog' })).toHaveCount(0)
  // The approval banner shows on the silently-adopted entry.
  await expect(page.getByRole('status').filter({ hasText: /approved/i })).toBeVisible()
  // The adopted entry still carries the submitted cover. Attribute
  // check, not visibility - img.example never resolves and EntryDetail
  // sets no onError, so the <img> (alt="", not role="img") stays
  // attached with its src regardless of the failed load.
  await expect(page.getByRole('main', { name: 'Entry detail' }).locator('img')).toHaveAttribute('src', cover)
  // Dismiss persists across a reload (server-stamped). The click fires a
  // fire-and-forget ack POST; the waitForResponse promise is set up
  // before the click so the response can never arrive unobserved, and
  // awaiting it before the reload stops the reload from cancelling the
  // in-flight ack.
  const ackResponse = page.waitForResponse((r) => r.url().includes('/submission/ack'))
  await page.getByRole('button', { name: 'Dismiss approval notice' }).click()
  await ackResponse
  await page.reload()
  await expect(page.getByRole('button', { name: 'Dismiss approval notice' })).toHaveCount(0)

  // The entry before the product - fixture teardown deletes the
  // submitter's account after this test returns, which is too late for
  // the product delete below (a still-referenced product 409s).
  await deleteEntry(submitter.api, entryId)
  await deleteCommunityProduct(adminContext.request, name)
  await adminContext.close()
})

test('community lane adopt: a search finds and adds the approved product', async ({ page, freshUser }) => {
  test.setTimeout(60_000)
  const user = await freshUser()
  // Named so it never contains the substring "community" - the
  // search result row's own community-origin badge reads exactly that
  // word, and a product name containing it would collide with the
  // badge text below.
  const name = `e2e Adopt Cart ${stamp}`
  const { entryId: arrangeEntryId } = await approvedCommunityProduct(user, await adminApi(), name)

  await loginAs(page, user.name)
  await page.getByRole('link', { name: 'Add', exact: true }).click()
  await page.getByRole('searchbox', { name: /search for games and hardware/i }).fill(name)
  await page.getByRole('button', { name: 'Search', exact: true }).click()
  await expect(page.getByText('community')).toBeVisible()
  // Scoped to the community-tagged row: it is the one marker that
  // unambiguously targets this pick's button among whatever else a
  // search can return.
  const communityResult = page.getByRole('region', { name: 'Search' }).locator('li').filter({ hasText: 'community' })
  await communityResult.getByRole('button', { name: `${name} on Super Nintendo Entertainment System` }).click()
  await page.getByRole('button', { name: 'Continue' }).click()
  await page.getByRole('button', { name: 'Add to collection' }).click()
  await expect(page).toHaveURL(/\/entries\//)
  const adoptedEntryId = entryIdFromURL(page.url())

  // Both entries before the product - fixture teardown deletes this
  // user's account after the test returns, too late for the product
  // delete below (a still-referenced product 409s).
  await deleteEntry(user.api, arrangeEntryId)
  await deleteEntry(user.api, adoptedEntryId)
  await deleteCommunityProduct(await adminApi(), name)
})

test('candidate sweep flags a colliding community product, and promote clears it', async ({ page, freshUser }) => {
  test.setTimeout(150_000)
  // Named "Chrono Trigger" on purpose: the real IGDB catalog already
  // carries its own Chrono Trigger on Super Nintendo Entertainment
  // System, so this community mint's name collides with a provider
  // identity - the collision is exactly what the candidate sweep looks
  // for. This test deletes everything it creates, freeing the identity
  // slot for the next run; the file-level afterAll mop above is a
  // crash-safety net for a run that does not get that far.
  const submitter = await freshUser()
  const adopter = await freshUser()
  const { entryId: submitterEntryId, productId } = await approvedCommunityProduct(submitter, await adminApi(), 'Chrono Trigger')
  // Mirrors the add wizard's own community branch (ConfirmStep.tsx):
  // picking a community result skips resolve entirely and creates the
  // entry straight off the picked product id.
  const { id: adopterEntryId } = await createEntry(adopter.api, { product_id: productId })

  await loginAs(page, 'admin')
  await page.getByRole('link', { name: 'Admin', exact: true }).click()
  await page.getByRole('button', { name: 'Trigger catalog refresh' }).click()
  await expect(page.getByText('Refresh started.')).toBeVisible()
  const candidates = page.getByRole('region', { name: 'Promote candidates' })
  const candRow = candidates.locator('tbody tr').filter({ hasText: 'Chrono Trigger' })
  // The refresh is detached (202) and its candidate sweep is the last
  // phase, so the flag lands seconds later. The admin queries carry a 5
  // minute staleTime, so a same-session remount (tab switch, SPA nav)
  // repaints from cache without refetching; only a full reload rebuilds
  // the QueryClient and forces a fresh candidates read. Poll on reload
  // until the sweep flags the community product.
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

  // Cleanup: entries before the product - a still-referenced product
  // refuses to delete (409 product_referenced).
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
  // The submitter's profile must be non-private for the queue's
  // submitter-handle link to render at all (SubmitterCell falls back to
  // plain text for a private card) - unlisted is enough (reachable by a
  // direct link, not listed in search); the account itself is thrown
  // away at teardown, so there is nothing to restore.
  await setProfile(submitter.api, { profile_visibility: 'unlisted' })

  await loginAs(page, submitter.name)
  const { id: entryId } = await addCustom(page, name, undefined, 'ntsc_j', 'Garage Team')
  // The custom credit fact renders on the entry itself right away.
  await expect(page.getByText('Developed by Garage Team')).toBeVisible()
  await page.getByRole('button', { name: 'Submit to catalog' }).click()
  await expect(page.getByText(/waiting for review/i)).toBeVisible()

  // The queue row shows the labeled region and a live submitter handle
  // link; the review panel keeps the region prefilled.
  await logout(page)
  await loginAs(page, 'admin')
  await page.getByRole('link', { name: 'Admin', exact: true }).click()
  await page.getByRole('tab', { name: 'Submissions' }).click()
  // Scoped to the queue's own region for the same reason the approve
  // test above does: once approved, this same-named product also
  // renders in the community products cleanup list right below it.
  const queue = page.getByRole('region', { name: 'Catalog submissions' })
  const row = queue.locator('tbody tr').filter({ hasText: name })
  await expect(row).toContainText('NTSC-J')
  await expect(row.getByRole('link')).toHaveAttribute('href', /^\/u\//)
  await row.getByRole('button', { name: 'Review' }).click()
  const panel = page.locator(`[aria-label="Review ${name}"]`)
  await expect(panel.getByLabel('Region')).toHaveValue('ntsc_j')
  await expect(panel.getByLabel('Developers 1', { exact: true })).toHaveValue('Garage Team')
  await panel.getByRole('button', { name: 'Approve as new product' }).click()
  await expect(queue.locator('tbody tr').filter({ hasText: name })).toHaveCount(0)

  // A different, minted viewer finds the new community product; its
  // region tag reads back the approved value, and the wizard's own
  // region default already carries it too - no submit needed to prove
  // that (defaultDetails seeds it straight off the community pick, per
  // AddWizard's community branch), so the journey stops here.
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

  // Cleanup: the entry before the product - fixture teardown deletes the
  // submitter's account (and profile with it) after this test returns,
  // too late for the product delete below (a still-referenced product
  // 409s). The still-community product this leg minted (its own name,
  // not the shared Chrono Trigger identity slot) needs its own delete.
  await deleteEntry(submitter.api, entryId)
  await deleteCommunityProduct(await adminApi(), name)
})

test('admin deletes a community product from the cleanup list', async ({ page, freshUser }) => {
  test.setTimeout(60_000)
  const submitter = await freshUser()
  const name = `e2e Cleanup Cart ${stamp}`
  const { entryId } = await approvedCommunityProduct(submitter, await adminApi(), name)
  // The entry is deleted first so the product is unreferenced by the
  // time the delete button below is clicked - otherwise the list's own
  // 409 product_referenced message would fire instead of the row
  // disappearing.
  await deleteEntry(submitter.api, entryId)

  await loginAs(page, 'admin')
  await page.getByRole('link', { name: 'Admin', exact: true }).click()
  await page.getByRole('tab', { name: 'Submissions' }).click()
  const list = page.getByRole('region', { name: 'Community products' })
  const row = list.locator('tbody tr').filter({ hasText: name })
  const loadMore = list.getByRole('button', { name: 'Load more' })
  // The list sorts oldest-updated first, so a just-minted product can
  // sit past the first loaded page; poll "Load more" until the row
  // surfaces or the button itself disappears (every page loaded).
  await expect(async () => {
    if ((await row.count()) === 0 && (await loadMore.count()) > 0) await loadMore.click()
    await expect(row).toBeVisible({ timeout: 2_000 })
  }).toPass({ timeout: 30_000 })
  await row.getByRole('button', { name: 'Delete' }).click()
  await expect(row).toHaveCount(0)
})
