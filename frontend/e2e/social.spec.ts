import { expect, request, test } from '@playwright/test'
import type { APIRequestContext, Page } from '@playwright/test'

// Serial social journey: alice publishes her Backlog shelf, bob
// discovers it, follows, likes, and comments, both feeds reflect it,
// then alice unpublishes and republishes it. The five tests below
// share one page/session (test.describe.configure + the beforeAll
// page below) rather than each logging in fresh, because several
// steps deliberately continue the previous step's identity - the
// handle-cooldown leg in particular only makes sense as a live
// continuation of the same alice session flow 1 starts.
const stamp = Date.now()
const UNDO_WINDOW_MS = 7_000 // frontend/src/components/social/useCommentDelete.ts

// Programmatic dev-provider login: one GET seals the session cookie
// and redirects home, a single /api/auth/* hit. Copied from
// submissions.spec.ts.
async function login(page: Page, fixture: string) {
  await page.goto(`/api/auth/login?provider=dev&user=${fixture}`)
  await expect(page.getByRole('navigation', { name: 'Primary' })).toBeVisible()
}

// Role switches drop the session cookie instead of hunting a logout
// control; the next login mints a fresh session. Copied from
// submissions.spec.ts.
async function logout(page: Page) {
  await page.context().clearCookies()
}

// apiLogin drives the dev provider on an APIRequestContext, landing
// the sealed session cookie in the context jar. Copied from
// submissions.spec.ts.
async function apiLogin(ctx: APIRequestContext, fixture: string) {
  await ctx.get(`/api/auth/login?provider=dev&user=${fixture}`)
}

// Settle the shared /api/auth/* bucket (gateway limit: 20 requests per
// 60s per IP, a fixed window - deploy/charts/bff/values.yaml
// authPerMinute) before handing off to submissions.spec.ts, the
// suite's own heaviest file against that same bucket (five fresh
// logins in its approve test alone). This file's five logins plus the
// two apiLogins in the restore afterAll below land close together near
// the end of the run; without a settle they can still be inside the
// window when submissions.spec.ts's logins start, and a live run
// confirmed exactly that (429 on its "approve..." test's login). The
// same pattern collection-journey.spec.ts and currency.spec.ts already
// use to protect the specs that run after them.
const AUTH_SETTLE_MS = 62_000

test.describe.configure({ mode: 'serial' })

let page: Page
// Captured in "alice publishes" and read by every later test/hook;
// aliceHandle in particular is whatever alice's handle actually is
// when this spec starts (read off the header chip, never assumed) so
// the cooldown leg and the final restore always target the real
// original, not a guessed literal.
let aliceHandle = ''
let aliceUserId = ''
let shelfId = ''
let shelfName = ''
let shelfParams: Record<string, unknown> = {}
let entryId = ''
// Captured in "bob discovers and engages" (off the create-comment
// response) so the restore afterAll can fall back to deleting it
// directly if this test fails before its own delete/undo legs finish.
let commentId = ''

test.beforeAll(async ({ browser }) => {
  page = await browser.newPage()
})

// API-context restore: alice's profile and shelf go back to private
// and her handle back to the original; bob's like, comment, and
// follow come off. Everything here is idempotent and independently
// guarded, so a run that crashed partway still leaves the next run a
// clean start. The AUTH_SETTLE_MS sleep (see above) runs as the last
// step of this same hook, below, rather than in a separately-
// registered afterAll: Playwright fires afterAll hooks in
// registration order, so a standalone settle hook registered ahead of
// this restore would let the restore's own two apiLogin hits land
// immediately after the settle - the exact window it exists to
// protect.
test.afterAll(async () => {
  test.setTimeout(AUTH_SETTLE_MS + 70_000) // 60s restore budget + 10s buffer + 62s settle
  const baseURL = process.env.BFF_URL ?? 'http://localhost:8090'
  let ctx: APIRequestContext | undefined
  try {
    ctx = await request.newContext({ baseURL })
    try {
      await apiLogin(ctx, 'alice')
      await ctx.patch('/api/me', { data: { profile_visibility: 'private' } })
      if (shelfId) {
        const put = await ctx.put(`/api/views/${shelfId}`, {
          data: { name: shelfName, params: shelfParams, visibility: 'private' },
        })
        console.log(`teardown: shelf ${shelfId} -> private (${put.status()})`)
      }
      if (entryId) {
        const del = await ctx.delete(`/api/entries/${entryId}`)
        console.log(`teardown: entry ${entryId} -> ${del.status()}`)
      }
      if (aliceHandle) {
        const me = await ctx.get('/api/me')
        if (me.ok()) {
          const body = (await me.json()) as { handle?: string }
          if (body.handle && body.handle !== aliceHandle) {
            const fix = await ctx.patch('/api/me', { data: { handle: aliceHandle } })
            console.log(`teardown: handle ${body.handle} -> ${aliceHandle} (${fix.status()})`)
          }
        }
      }
    } catch (err) {
      console.log('teardown: alice restore skipped:', err)
    }
    try {
      await apiLogin(ctx, 'bob')
      if (shelfId) await ctx.delete(`/api/social/likes/${shelfId}`)
      if (commentId) {
        // Guarded fallback: a no-op 404 when the in-test delete/undo
        // legs already tombstoned it (the common case), a real
        // delete when this test failed before reaching them.
        const del = await ctx.delete(`/api/comments/${commentId}`)
        console.log(`teardown: comment ${commentId} -> ${del.status()}`)
      }
      if (aliceUserId) await ctx.delete(`/api/social/follows/${aliceUserId}`)
    } catch (err) {
      console.log('teardown: bob restore skipped:', err)
    }
  } catch (err) {
    console.log('teardown: residue mop skipped (best-effort):', err)
  } finally {
    await ctx?.dispose()
    await page?.close()
  }

  // Settle last, after all restore traffic above (see AUTH_SETTLE_MS
  // and the comment at the top of this hook).
  await new Promise((resolve) => setTimeout(resolve, AUTH_SETTLE_MS))
})

test('alice publishes', async () => {
  test.setTimeout(120_000)
  await login(page, 'alice')

  const me = await page.request.get('/api/me')
  expect(me.ok()).toBeTruthy()
  aliceUserId = ((await me.json()) as { id: string }).id

  // Seed one backlog item so the shared shelf has a numbered row to
  // prove against in "bob discovers and engages" - self-contained
  // rather than assuming this long-lived stack already holds one.
  const entryRes = await page.request.post('/api/entries', {
    data: {
      display_name: `Social E2E Item ${stamp}`,
      item_type: 'game',
      region: 'ntsc_u',
      packaging: 'loose',
    },
  })
  expect(entryRes.ok()).toBeTruthy()
  entryId = ((await entryRes.json()) as { id: string }).id

  // Every account is seeded with its "Backlog" saved view the first
  // time its views are listed; capture id/name/params so the final
  // restore can PUT the exact same name/params back with visibility
  // reverted (a full-replacement PUT, like the frontend's own).
  const viewsRes = await page.request.get('/api/views')
  expect(viewsRes.ok()).toBeTruthy()
  const views = ((await viewsRes.json()) as {
    views: { id: string; name: string; params: Record<string, unknown> }[]
  }).views
  const backlog = views.find((v) => v.name === 'Backlog')
  if (!backlog) throw new Error('Backlog view not found')
  shelfId = backlog.id
  shelfName = backlog.name
  shelfParams = backlog.params

  await page.goto('/account')
  await page.getByRole('radio', { name: 'Listed - appears in Explore and search' }).click()
  await page.getByRole('button', { name: 'Save', exact: true }).click()
  await expect(page.getByText('Saved.')).toBeVisible()

  const chipText = await page.getByRole('link', { name: 'Account' }).getByText(/^@/).textContent()
  aliceHandle = (chipText ?? '').replace('@', '').trim()
  expect(aliceHandle.length).toBeGreaterThan(0)

  await page.goto('/collection')
  await page.getByRole('tab', { name: 'Shelves' }).click()
  const shelfManager = page.getByRole('region', { name: 'Manage shelves' })
  const backlogRow = shelfManager.getByRole('listitem').filter({ hasText: 'Backlog' })
  await backlogRow.getByRole('button', { name: 'Listed', exact: true }).click()
  await expect(backlogRow.getByRole('button', { name: 'Copy link' })).toBeVisible()
})

test('handle cooldown live', async () => {
  test.setTimeout(60_000)
  await page.goto('/account')
  const handleInput = page.getByLabel('Handle')

  const first = `Alice_e2e_${stamp}`
  await handleInput.fill(first)
  await page.getByRole('button', { name: 'Save', exact: true }).click()
  await expect(page.getByText('Saved.')).toBeVisible()

  const second = `Alice_e2e2_${stamp}`
  await handleInput.fill(second)
  await page.getByRole('button', { name: 'Save', exact: true }).click()
  await expect(page.getByRole('alert')).toHaveText('Handle changed too recently - try again later.')

  // The live user pod's HANDLE_CHANGE_COOLDOWN is a 5s dev-only
  // override (Tiltfile); clear it before the restoring change.
  await page.waitForTimeout(5_500)

  await handleInput.fill(aliceHandle)
  await page.getByRole('button', { name: 'Save', exact: true }).click()
  await expect(page.getByText('Saved.')).toBeVisible()
})

test('bob discovers and engages', async () => {
  test.setTimeout(120_000)
  await logout(page)
  await login(page, 'bob')

  await page.goto('/explore')
  const explore = page.getByRole('main', { name: 'Explore' })
  const aliceCard = explore.getByRole('listitem').filter({ hasText: 'Backlog' }).filter({ hasText: aliceHandle })
  await expect(aliceCard).toBeVisible()

  await page.getByRole('searchbox', { name: 'Search for people' }).fill(aliceHandle)
  const results = page.getByRole('list', { name: 'Search results' })
  await results.getByRole('link', { name: `@${aliceHandle}` }).click()
  await expect(page).toHaveURL(new RegExp(`/u/${aliceHandle}$`))

  await page.getByRole('button', { name: 'Follow', exact: true }).click()
  await expect(page.getByRole('button', { name: 'Following' })).toBeVisible()

  const shelves = page.getByRole('region', { name: 'Shelves' })
  await shelves.getByRole('link', { name: 'Backlog', exact: true }).click()
  await expect(page).toHaveURL(new RegExp('/shelves/Backlog$'))

  // Backlog sorts by rank (the shelf's own stored params), so the
  // shared read renders a numbered rank column; shared reads never
  // link into /entries/ (the viewer does not own these rows).
  const entriesRegion = page.getByRole('region', { name: 'Entries' })
  await expect(entriesRegion.locator('a[href*="/entries/"]')).toHaveCount(0)
  await expect(entriesRegion.getByRole('columnheader', { name: '#' })).toBeVisible()
  const itemRow = entriesRegion.getByRole('row', { name: new RegExp(`Social E2E Item ${stamp}`) })
  await expect(itemRow.getByRole('cell').first()).toHaveText(/^\d+$/)

  // The long-lived dev stack carries ambient likes from real usage
  // (owner sessions ride the same seeded shelves), so assert the
  // delta from this journey's own click, never an absolute count.
  const likeBtn = page.getByRole('button', { name: 'Like', exact: true })
  const likedBefore = Number((await likeBtn.textContent())?.replace(/\D/g, '') ?? '0')
  await likeBtn.click()
  const unlike = page.getByRole('button', { name: 'Unlike' })
  await expect(unlike).toBeVisible()
  await expect(unlike).toHaveText(new RegExp('^\\u2665\\s*' + (likedBefore + 1) + '$'))

  const commentText = `e2e comment ${stamp}`
  await page.getByLabel('Add a comment').fill(commentText)
  // Capture the created comment's id off the POST response (armed
  // before the click, so the response can never arrive unobserved)
  // for the restore afterAll's guarded fallback delete.
  const commentPost = page.waitForResponse(
    (r) => r.url().includes(`/api/shelves/${shelfId}/comments`) && r.request().method() === 'POST' && r.status() === 201,
  )
  await page.getByRole('button', { name: 'Post', exact: true }).click()
  commentId = ((await (await commentPost).json()) as { id: string }).id
  await expect(page.getByText(commentText)).toBeVisible()

  // Delete, then Undo within the toast: the comment must still be
  // there (the DELETE is deferred client-side, not yet committed).
  const deleteButton = page.getByRole('button', { name: `Delete your comment: ${commentText}` })
  await deleteButton.click()
  await expect(page.getByRole('status')).toContainText('Comment deleted')
  await page.getByRole('button', { name: 'Undo' }).click()
  await expect(page.getByText(commentText)).toBeVisible()

  // Delete again and this time let the undo window actually expire:
  // the commit fires, the comment (and its toast) are truly gone.
  await page.getByRole('button', { name: `Delete your comment: ${commentText}` }).click()
  await expect(page.getByRole('status')).toContainText('Comment deleted')
  await page.waitForTimeout(UNDO_WINDOW_MS + 1_500)
  await expect(page.getByText(commentText)).toHaveCount(0)
  await expect(page.getByRole('status')).toHaveCount(0)
})

test('feeds', async () => {
  test.setTimeout(60_000)
  await page.goto('/feed')
  const bobFeed = page.getByRole('main', { name: 'Feed' })
  // Following is the default tab: bob follows only alice, so her
  // publish is expected without switching tabs.
  // Ambient rows from real usage can share these feeds; every
  // assertion scopes to rows this journey's own actors produced.
  await expect(bobFeed.getByRole('listitem').filter({ hasText: 'published' }).first()).toBeVisible()
  await expect(bobFeed.getByRole('link', { name: 'Backlog', exact: true }).first()).toBeVisible()

  await logout(page)
  await login(page, 'alice')
  await page.goto('/feed')
  await page.getByRole('tab', { name: 'You', exact: true }).click()
  const aliceFeed = page.getByRole('main', { name: 'Feed' })
  // bob's follow and like both target alice; his comment already
  // died in the previous test, so its activity row died with it.
  const bobRows = (verb: string) =>
    aliceFeed.getByRole('listitem').filter({ hasText: verb }).filter({ hasText: '@Bob_Fixture' })
  await expect(bobRows('followed').first()).toBeVisible()
  await expect(bobRows('liked').first()).toBeVisible()
  await expect(bobRows('commented on')).toHaveCount(0)
})

test('unpublish hides', async () => {
  test.setTimeout(60_000)
  await page.goto('/collection')
  await page.getByRole('tab', { name: 'Shelves' }).click()
  const shelfManager = page.getByRole('region', { name: 'Manage shelves' })
  const backlogRow = shelfManager.getByRole('listitem').filter({ hasText: 'Backlog' })

  await backlogRow.getByRole('button', { name: 'Private' }).click()
  await expect(backlogRow.getByRole('button', { name: 'Copy link' })).toHaveCount(0)

  await page.goto('/explore')
  const explore = page.getByRole('main', { name: 'Explore' })
  const aliceCard = explore.getByRole('listitem').filter({ hasText: 'Backlog' }).filter({ hasText: aliceHandle })
  await expect(aliceCard).toHaveCount(0)

  await page.goto('/collection')
  await page.getByRole('tab', { name: 'Shelves' }).click()
  await backlogRow.getByRole('button', { name: 'Listed', exact: true }).click()
  await expect(backlogRow.getByRole('button', { name: 'Copy link' })).toBeVisible()

  await page.goto('/explore')
  await expect(aliceCard).toBeVisible()
})
