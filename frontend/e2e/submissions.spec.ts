import { expect, request, test } from '@playwright/test'
import type { APIRequestContext, Page } from '@playwright/test'

// Catalog-submissions journey across three fixtures. task e2e grants
// the admin role up front; every login below is fresh. The shared
// stamp isolates the reject leg; the approve leg deliberately uses
// the stub game name "Chrono Trigger" so the candidate sweep can
// flag it - the run deletes everything it creates, so the provider
// identity slot it promotes into is freed for the next run.
const stamp = `e2e-sub-${Date.now()}`

// Pace the shared /api/* bucket. The gateway caps /api/* at 300 requests per
// 60s per IP (deploy/charts/bff/values.yaml apiPerMinute) and the whole
// serial suite shares one bucket. With the old 63s drains gone, this is the
// last and one of the heaviest specs; its approve leg drives five fresh
// logins and then a reload-until-flagged poll loop that refetches the whole
// admin page each pass. A short settle before that poll loop lets the multi-
// login front half age out of the window so the suite's own worst 60s window
// stays well under the cap (worst window ~270, leaving room for a human on the
// same stack). Seconds-scale, not the old minute-long drains.
const API_PACE_MS = 12_000
async function paceApiBucket() {
  await new Promise((resolve) => setTimeout(resolve, API_PACE_MS))
}

// Programmatic dev-provider login: one GET seals the session cookie and
// redirects home, a single /api/auth/* hit (the old /login UI helper cost
// two - a providers fetch plus the redirect). The gateway caps
// /api/auth/* at 20 requests per 60s per IP and the whole serial suite
// shares one; at one hit per login every 60s window stays well under the
// cap with no drains. login.spec still exercises the /login UI itself.
async function login(page: Page, fixture: string) {
  await page.goto(`/api/auth/login?provider=dev&user=${fixture}`)
  await expect(page.getByRole('navigation', { name: 'Primary' })).toBeVisible()
}

// Role switches drop the session cookie instead of hunting a logout
// control; the next login mints a fresh session.
async function logout(page: Page) {
  await page.context().clearCookies()
}

function acceptNext(page: Page) {
  page.once('dialog', (d) => void d.accept())
}

// region is a trailing optional param (rather than folding into an
// options object) so the two existing positional call sites below need
// no changes; only the new region leg passes it.
async function addCustom(page: Page, name: string, cover?: string, region?: string) {
  await page.getByRole('link', { name: 'Add', exact: true }).click()
  await page.getByRole('button', { name: /custom item/i }).click()
  await page.getByLabel('Name').fill(name)
  await page.getByLabel('Platform').fill('snes')
  await page.getByRole('button', { name: 'Super Nintendo Entertainment System' }).click()
  if (region) await page.getByLabel('Region').selectOption(region)
  if (cover) await page.getByLabel(/cover image link/i).fill(cover)
  await page.getByRole('button', { name: 'Continue' }).click()
  await page.getByRole('button', { name: 'Continue' }).click()
  await page.getByRole('button', { name: 'Add to collection' }).click()
  await expect(page).toHaveURL(/\/entries\//)
  return page.url()
}

// apiLogin drives the dev provider through its redirect chain on an
// APIRequestContext, landing the sealed __Host-vg_session cookie in the
// context jar. Like the page login helper above, it costs one
// /api/auth/* hit. Re-calling it on the same context swaps the active
// fixture; the cookie name is constant so the new seal overwrites the
// old (verified against the live stack).
async function apiLogin(ctx: APIRequestContext, fixture: string) {
  await ctx.get(`/api/auth/login?provider=dev&user=${fixture}`)
}

// deleteResidualEntries removes the logged-in owner's spec-created
// entries. The approve leg names its entry "Chrono Trigger" (fixed, so
// the sweep can flag it); the reject leg stamps its entry "Reject Cart
// <stamp>". Both are unmistakable spec data, so the filter never
// touches unrelated fixture entries. Entries go before their product:
// a promoted product will not delete while an entry still references it
// (409 product_referenced). An empty list is success.
async function deleteResidualEntries(ctx: APIRequestContext) {
  const res = await ctx.get('/api/entries?limit=500')
  if (!res.ok()) return
  const body = (await res.json()) as { entries?: { id: string; display_name: string }[] }
  for (const entry of body.entries ?? []) {
    if (entry.display_name !== 'Chrono Trigger' && !entry.display_name.includes(stamp)) continue
    const del = await ctx.delete(`/api/entries/${entry.id}`)
    console.log(`teardown: entry ${entry.id} "${entry.display_name}" -> ${del.status()}`)
  }
}

// deleteResidualProducts (admin session) removes the "Chrono Trigger"
// product the approve leg minted, in whichever state the run died:
// still community (it surfaces in the search community lane) or already
// promoted (it surfaces in the unmatched worklist). The three baseline
// matched "Chrono Trigger" products are provider-identified with a
// pricecharting mapping, so they appear in neither list and are never
// touched; the admin delete refuses matched or still-referenced
// products anyway (409), which best-effort treats as success. Empty
// lists and 404s are success (idempotent).
async function deleteResidualProducts(ctx: APIRequestContext) {
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

// deleteCommunityProduct (admin session) removes a still-community
// product by its exact name - the region leg's own mint, distinct from
// the "Chrono Trigger" identity slot the other tests in this file
// share, so it needs its own by-name lookup rather than
// deleteResidualProducts' hardcoded name. Best-effort like the rest of
// this file's cleanup: logged, not asserted, so a residual delete
// failure never masks the journey itself.
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

// Self-healing safety net. The in-test UI cleanup at the end of the
// approve test doubles as UI-delete coverage and clears everything on a
// green run, so this teardown then finds nothing. But a mid-test
// failure strands the spec-created entities (alice/bob "Chrono Trigger"
// entries, the minted community product, and - once promoted - the
// provider product on the SNES slot), which block the next run: a
// second community "Chrono Trigger" turns the community-lane adopt into
// a strict-mode multiple-match, and the count assertions expect zero.
// This re-drives the documented mop over the API (each entry deleted by
// its owner, then the product by admin) so a failed run leaves the
// stack as clean as a green one.
//
// These three apiLogin calls cost one /api/auth/* hit each. Every login
// in the suite now uses that 1-request posture, so the run's trailing
// 60s window stays far under the gateway's 20-per-60s cap and this mop
// needs no drain ahead of it. Everything is guarded so a teardown fault
// can never mask the test result; individual deletes log their status
// and continue, and non-2xx (404 gone, 409 still-referenced) is treated
// as success.
test.afterAll(async () => {
  test.setTimeout(60_000)
  const baseURL = process.env.BFF_URL ?? 'http://localhost:8090'
  let ctx: APIRequestContext | undefined
  try {
    ctx = await request.newContext({ baseURL })
    for (const owner of ['alice', 'bob']) {
      try {
        await apiLogin(ctx, owner)
        await deleteResidualEntries(ctx)
      } catch (err) {
        console.log(`teardown: ${owner} entry mop skipped:`, err)
      }
    }
    try {
      await apiLogin(ctx, 'admin')
      await deleteResidualProducts(ctx)
    } catch (err) {
      console.log('teardown: product mop skipped:', err)
    }
  } catch (err) {
    console.log('teardown: residue mop skipped (best-effort):', err)
  } finally {
    await ctx?.dispose()
  }
})

test('reject round trip: submit, admin rejects, submitter reads the reason', async ({ page }) => {
  test.setTimeout(120_000)
  await login(page, 'alice')
  const entryURL = await addCustom(page, `Reject Cart ${stamp}`)

  await page.getByRole('button', { name: 'Submit to catalog' }).click()
  await expect(page.getByText(/waiting for review/i)).toBeVisible()
  await logout(page)

  await login(page, 'admin')
  await page.getByRole('link', { name: 'Admin', exact: true }).click()
  await page.getByRole('tab', { name: 'Submissions' }).click()
  const row = page.locator('tbody tr').filter({ hasText: `Reject Cart ${stamp}` })
  await row.getByRole('button', { name: 'Review' }).click()
  const panel = page.locator(`[aria-label="Review Reject Cart ${stamp}"]`)
  await panel.getByLabel('Rejection reason').fill('not a shared item')
  await panel.getByRole('button', { name: 'Reject' }).click()
  await expect(page.locator('tbody tr').filter({ hasText: `Reject Cart ${stamp}` })).toHaveCount(0)
  await logout(page)

  await login(page, 'alice')
  await page.goto(entryURL)
  await expect(page.getByText(/rejected: not a shared item/i)).toBeVisible()
  await expect(page.getByRole('button', { name: 'Resubmit to catalog' })).toBeVisible()
  acceptNext(page)
  await page.getByRole('button', { name: 'Delete entry' }).click()
  await expect(page).toHaveURL(/\/collection$/)
})

test('approve, community lane, candidate sweep, promote, cleanup', async ({ page }) => {
  test.setTimeout(300_000)

  // --- alice submits her custom Chrono Trigger repro.
  await login(page, 'alice')
  const aliceEntryURL = await addCustom(page, 'Chrono Trigger', 'https://img.example/ct.jpg')
  await page.getByRole('button', { name: 'Submit to catalog' }).click()
  await expect(page.getByText(/waiting for review/i)).toBeVisible()
  await logout(page)

  // --- admin approves-new with the curated form.
  await login(page, 'admin')
  await page.getByRole('link', { name: 'Admin', exact: true }).click()
  await page.getByRole('tab', { name: 'Submissions' }).click()
  // Scoped to the queue's own region: the Submissions tab also renders the
  // community products cleanup list right below it, and approve-new mints
  // this same-named product as a community row there, so an unscoped
  // page-wide "Chrono Trigger" tbody-row locator would double-count once
  // that second table refetches after the verdict.
  const queue = page.getByRole('region', { name: 'Catalog submissions' })
  await queue
    .locator('tbody tr')
    .filter({ hasText: 'Chrono Trigger' })
    .first()
    .getByRole('button', { name: 'Review' })
    .click()
  const review = page.locator('[aria-label="Review Chrono Trigger"]')
  await expect(review.getByLabel('Name')).toHaveValue('Chrono Trigger')
  await review.getByRole('button', { name: 'Approve as new product' }).click()
  await expect(queue.locator('tbody tr').filter({ hasText: 'Chrono Trigger' })).toHaveCount(0)
  await logout(page)

  // --- alice's entry is product-backed now (the block is custom-only).
  await login(page, 'alice')
  await page.goto(aliceEntryURL)
  await expect(page.getByText(/custom item/)).toHaveCount(0)
  await expect(page.getByRole('button', { name: 'Submit to catalog' })).toHaveCount(0)
  // S1: the approval banner shows on the silently-adopted entry.
  await expect(page.getByRole('status')).toContainText(/approved/i)
  // S2: the adopted entry still carries alice's submitted cover. Attribute
  // check, not visibility - img.example never resolves and EntryDetail sets
  // no onError, so the <img> (alt="", not role="img") stays attached with
  // its src regardless of the failed load.
  await expect(page.getByRole('main', { name: 'Entry detail' }).locator('img')).toHaveAttribute(
    'src',
    'https://img.example/ct.jpg',
  )
  // Dismiss persists across a reload (server-stamped). The click fires a
  // fire-and-forget ack POST; the waitForResponse promise is set up before
  // the click so the response can never arrive unobserved, and awaiting it
  // before the reload stops the reload from cancelling the in-flight ack.
  const ackResponse = page.waitForResponse((r) => r.url().includes('/submission/ack'))
  await page.getByRole('button', { name: 'Dismiss approval notice' }).click()
  await ackResponse
  await page.reload()
  await expect(page.getByRole('button', { name: 'Dismiss approval notice' })).toHaveCount(0)
  await logout(page)

  // --- bob finds it through the community lane and adopts it.
  await login(page, 'bob')
  await page.getByRole('link', { name: 'Add', exact: true }).click()
  await page.getByRole('searchbox', { name: /search for games and hardware/i }).fill('chrono trigger')
  await page.getByRole('button', { name: 'Search', exact: true }).click()
  await expect(page.getByText('community')).toBeVisible()
  // The real IGDB catalog already carries its own "Chrono Trigger" with a
  // Super Nintendo Entertainment System platform among its options, so its
  // provider chip shares the exact same aria-label as the community chip -
  // scope to the community-tagged row (the one distinguishing marker) to
  // pick the right one.
  const communityResult = page.getByRole('region', { name: 'Search' }).locator('li').filter({ hasText: 'community' })
  await communityResult.getByRole('button', { name: 'Chrono Trigger on Super Nintendo Entertainment System' }).click()
  await page.getByRole('button', { name: 'Continue' }).click()
  await page.getByRole('button', { name: 'Add to collection' }).click()
  await expect(page).toHaveURL(/\/entries\//)
  const bobEntryURL = page.url()
  await logout(page)

  // --- admin: refresh -> the sweep flags the community product ->
  // promote it from the candidates worklist.
  await login(page, 'admin')
  await page.getByRole('link', { name: 'Admin', exact: true }).click()
  await page.getByRole('button', { name: 'Trigger catalog refresh' }).click()
  await expect(page.getByText('Refresh started.')).toBeVisible()
  // Settle before the reload storm below: the refresh is detached (202) and
  // runs server-side during this wait, so the front-half login traffic ages
  // out of the /api/* window and the poll loop starts against a cool bucket
  // (and usually flags on its first reload). See API_PACE_MS above.
  await paceApiBucket()
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

  // --- cleanup: entries first, then the promoted product - now
  // provider and unmatched, so it sits in the unmatched worklist and
  // the standard MappingFix delete mops it.
  await logout(page)
  await login(page, 'alice')
  await page.goto(aliceEntryURL)
  acceptNext(page)
  await page.getByRole('button', { name: 'Delete entry' }).click()
  await logout(page)
  await login(page, 'bob')
  await page.goto(bobEntryURL)
  acceptNext(page)
  await page.getByRole('button', { name: 'Delete entry' }).click()
  await logout(page)
  await login(page, 'admin')
  await page.getByRole('link', { name: 'Admin', exact: true }).click()
  const worklist = page.getByRole('region', { name: 'Unmatched products' })
  const promotedRow = worklist.locator('tbody tr').filter({ hasText: 'Chrono Trigger' })
  await expect(promotedRow.first()).toBeVisible()
  await promotedRow.first().getByRole('button', { name: 'Fix mapping' }).click()
  acceptNext(page)
  await worklist.getByRole('button', { name: 'Delete' }).click()
  await expect(worklist.locator('tbody tr').filter({ hasText: 'Chrono Trigger' })).toHaveCount(0)
})

test('custom entry via the platform picker lands filterable in the collection filter', async ({ page }) => {
  test.setTimeout(120_000)
  await login(page, 'bob')
  const url = await addCustom(page, `Picker Cart ${stamp}`)
  await page.goto('/collection')
  // The filter panel starts collapsed; the facet chips render only once opened.
  await page.getByRole('button', { name: /^Filters/ }).click()
  // The SNES facet exists because the picked entry carries platform_igdb_id.
  const filter = page.getByRole('group', { name: /platform/i })
  await expect(filter.getByText('Super Nintendo Entertainment System')).toBeVisible()
  // Cleanup: delete the entry (teardown also mops residue).
  await page.goto(url)
  acceptNext(page)
  await page.getByRole('button', { name: 'Delete entry' }).click()
})

test('non-admin never sees the submissions surface', async ({ page }) => {
  await login(page, 'bob')
  await expect(page.getByRole('link', { name: 'Admin' })).toHaveCount(0)
  // The page guard redirects home, which then resolves through the
  // landing-page redirect to /feed.
  await page.goto('/admin')
  await expect(page).toHaveURL('/feed')
})

test('custom add region flows through the queue, review, approval, and the next add wizard', async ({ page }) => {
  test.setTimeout(120_000)
  const name = `Region Cart ${stamp}`

  // --- alice submits a custom item with an explicit known region.
  // Her profile must be non-private for the queue's submitter-handle
  // link to render at all (SubmitterCell falls back to plain text for
  // a private card, see below) - unlisted is enough (reachable by a
  // direct link, not listed in search) and is restored at the end.
  await login(page, 'alice')
  await page.request.patch('/api/me', { data: { profile_visibility: 'unlisted' } })
  const entryURL = await addCustom(page, name, undefined, 'ntsc_j')
  await page.getByRole('button', { name: 'Submit to catalog' }).click()
  await expect(page.getByText(/waiting for review/i)).toBeVisible()
  await logout(page)

  // --- admin: the queue row shows the labeled region and a live
  // submitter handle link; the review panel keeps the region prefilled.
  await login(page, 'admin')
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
  await panel.getByRole('button', { name: 'Approve as new product' }).click()
  await expect(queue.locator('tbody tr').filter({ hasText: name })).toHaveCount(0)
  await logout(page)

  // --- bob (a different user) finds the new community product; its
  // region tag reads back the approved value, and the wizard's own
  // region default already carries it too - no submit needed to prove
  // that (defaultDetails seeds it straight off the community pick, per
  // AddWizard's community branch), so the journey stops here.
  await login(page, 'bob')
  await page.getByRole('link', { name: 'Add', exact: true }).click()
  await page.getByRole('searchbox', { name: /search for games and hardware/i }).fill(name)
  await page.getByRole('button', { name: 'Search', exact: true }).click()
  const communityResult = page.getByRole('region', { name: 'Search' }).locator('li').filter({ hasText: 'community' })
  await expect(communityResult.getByText('NTSC-J')).toBeVisible()
  await communityResult.getByRole('button', { name: `${name} on Super Nintendo Entertainment System` }).click()
  await expect(page.getByLabel('Region')).toHaveValue('ntsc_j')
  await logout(page)

  // --- cleanup: alice's entry (the afterAll safety net above also
  // catches it by the stamp in its name, had this failed first), her
  // profile visibility, then the still-community product this leg
  // minted (its own name, not the shared Chrono Trigger identity slot
  // the other tests in this file use).
  await login(page, 'alice')
  await page.request.patch('/api/me', { data: { profile_visibility: 'private' } })
  await page.goto(entryURL)
  acceptNext(page)
  await page.getByRole('button', { name: 'Delete entry' }).click()
  await expect(page).toHaveURL(/\/collection$/)
  await logout(page)
  await login(page, 'admin')
  await deleteCommunityProduct(page.request, name)
})
