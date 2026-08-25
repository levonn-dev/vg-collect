import type { APIRequestContext } from '@playwright/test'
import { expect, loginAs, test, BASE_URL, type TestUser } from './fixtures'
import { createEntry, listViews, setProfile, setViewVisibility } from './seed'

// Owner/viewer flows share one test each (not per-assertion re-minting)
// to stay under the gateway's /api/auth/* budget (240/IP/min dev); the
// other two tests are single-identity. Every account starts private and
// social-empty, so counts asserted below are exact, not deltas.
test.use({ storageState: { cookies: [], origins: [] } })

const stamp = Date.now().toString(36)

// Every account is seeded with a Backlog view; shared lookup for
// publishedOwner and the UI-publish flow.
async function backlogView(api: APIRequestContext) {
  const views = await listViews(api)
  const backlog = views.find((v) => v.name === 'Backlog')
  if (!backlog) throw new Error('Backlog view not found')
  return backlog
}

// Publishes a listed profile and Backlog shelf via API; the UI-publish
// flow drives its own clicks instead of this helper.
async function publishedOwner(freshUser: () => Promise<TestUser>, stamp: string) {
  const owner = await freshUser()
  const entry = await createEntry(owner.api, { display_name: `Social Item ${stamp}` })
  const backlog = await backlogView(owner.api)
  await setViewVisibility(owner.api, backlog.id, backlog.name, backlog.params, 'listed')
  await setProfile(owner.api, { profile_visibility: 'listed' })
  const me = await owner.api.get('/api/me')
  expect(me.ok(), `read /api/me for ${owner.name}: ${me.status()}`).toBeTruthy()
  const handle = ((await me.json()) as { handle: string }).handle
  return { owner, entryId: entry.id, shelfId: backlog.id, handle }
}

test('the owner publishes a shelf, the viewer discovers, follows, likes, and comments, and both feeds reflect it', async ({
  page,
  freshUser,
}) => {
  const owner = await freshUser()
  // Stamped item via API gives the discovery leg a numbered row to prove against.
  await createEntry(owner.api, { display_name: `Social Item ${stamp}` })
  await loginAs(page, owner.name)

  await page.goto('/account')
  await page.getByRole('radio', { name: 'Listed - appears in Explore and search' }).click()
  await page.getByRole('button', { name: 'Save', exact: true }).click()
  await expect(page.getByText('Saved.')).toBeVisible()

  const chipText = await page.getByRole('link', { name: 'Account' }).getByText(/^@/).textContent()
  const handle = (chipText ?? '').replace('@', '').trim()
  expect(handle.length).toBeGreaterThan(0)

  await page.goto('/collection')
  await page.getByRole('tab', { name: 'Shelves' }).click()
  const shelfManager = page.getByRole('region', { name: 'Manage shelves' })
  const backlogRow = shelfManager.getByRole('listitem').filter({ hasText: 'Backlog' })
  await backlogRow.getByRole('button', { name: 'Listed', exact: true }).click()
  await expect(backlogRow.getByRole('button', { name: 'Copy link' })).toBeVisible()

  // Shelf id needed for the comment legs' response-wait match (UI
  // publish, not publishedOwner).
  const shelfId = (await backlogView(owner.api)).id

  const viewer = await freshUser()
  await loginAs(page, viewer.name)

  await page.goto('/explore')
  const explore = page.getByRole('main', { name: 'Explore' })
  const ownerCard = explore.getByRole('listitem').filter({ hasText: 'Backlog' }).filter({ hasText: handle })
  await expect(ownerCard).toBeVisible()

  await page.getByRole('searchbox', { name: 'Search for people' }).fill(handle)
  const results = page.getByRole('list', { name: 'Search results' })
  await results.getByRole('link', { name: `@${handle}` }).click()
  await expect(page).toHaveURL(new RegExp(`/u/${handle}$`))

  await page.getByRole('button', { name: 'Follow', exact: true }).click()
  await expect(page.getByRole('button', { name: 'Following' })).toBeVisible()

  const shelves = page.getByRole('region', { name: 'Shelves' })
  await shelves.getByRole('link', { name: 'Backlog', exact: true }).click()
  await expect(page).toHaveURL(new RegExp('/shelves/Backlog$'))

  // Shared reads never link into /entries/ (viewer doesn't own the
  // rows); Backlog renders a rank column instead.
  const entriesRegion = page.getByRole('region', { name: 'Entries' })
  await expect(entriesRegion.locator('a[href*="/entries/"]')).toHaveCount(0)
  await expect(entriesRegion.getByRole('columnheader', { name: '#' })).toBeVisible()
  const itemRow = entriesRegion.getByRole('row', { name: new RegExp(`Social Item ${stamp}`) })
  await expect(itemRow.getByRole('cell').first()).toHaveText(/^\d+$/)

  // Freshly minted owner has zero ambient likes, so the post-click count is exact.
  const likeBtn = page.getByRole('button', { name: 'Like', exact: true })
  await likeBtn.click()
  const unlike = page.getByRole('button', { name: 'Unlike' })
  await expect(unlike).toBeVisible()
  await expect(unlike).toHaveText(/^\u2665\s*1$/)

  // clock.install() only affects timers created after; the undo window
  // (useCommentDelete.ts, 7s) isn't set until the Delete click below.
  await page.clock.install()

  const commentText = `e2e comment ${stamp}`
  await page.getByLabel('Add a comment').fill(commentText)
  // Armed before the click: a response wait, not a sleep.
  const commentPost = page.waitForResponse(
    (r) => r.url().includes(`/api/shelves/${shelfId}/comments`) && r.request().method() === 'POST' && r.status() === 201,
  )
  await page.getByRole('button', { name: 'Post', exact: true }).click()
  const posted = (await (await commentPost).json()) as { id: string }
  expect(posted.id.length).toBeGreaterThan(0)
  await expect(page.getByText(commentText)).toBeVisible()

  // Comment must still exist: DELETE is deferred client-side until commit.
  const deleteButton = page.getByRole('button', { name: `Delete your comment: ${commentText}` })
  await deleteButton.click()
  await expect(page.getByRole('status')).toContainText('Comment deleted')
  await page.getByRole('button', { name: 'Undo' }).click()
  await expect(page.getByText(commentText)).toBeVisible()

  // This time let the undo window expire: commit fires, comment and toast are gone.
  await page.getByRole('button', { name: `Delete your comment: ${commentText}` }).click()
  await expect(page.getByRole('status')).toContainText('Comment deleted')
  await page.clock.fastForward(8_500)
  await expect(page.getByText(commentText)).toHaveCount(0)
  await expect(page.getByRole('status')).toHaveCount(0)

  // Follow/like/delete already happened above; both feeds need no
  // further arranging.
  await page.goto('/feed')
  const viewerFeed = page.getByRole('main', { name: 'Feed' })
  // Following is the default tab; viewer follows only the owner.
  await expect(viewerFeed.getByRole('listitem').filter({ hasText: 'published' }).first()).toBeVisible()
  await expect(viewerFeed.getByRole('link', { name: 'Backlog', exact: true }).first()).toBeVisible()

  const viewerMe = await viewer.api.get('/api/me')
  expect(viewerMe.ok(), `read /api/me for ${viewer.name}: ${viewerMe.status()}`).toBeTruthy()
  const viewerHandle = ((await viewerMe.json()) as { handle: string }).handle

  await loginAs(page, owner.name)
  await page.goto('/feed')
  await page.getByRole('tab', { name: 'You', exact: true }).click()
  const ownerFeed = page.getByRole('main', { name: 'Feed' })
  const viewerRows = (verb: string) =>
    ownerFeed.getByRole('listitem').filter({ hasText: verb }).filter({ hasText: `@${viewerHandle}` })
  await expect(viewerRows('followed').first()).toBeVisible()
  await expect(viewerRows('liked').first()).toBeVisible()
  await expect(viewerRows('commented on')).toHaveCount(0)
})

test('shelf visibility round-trips through explore, and a logged-out visitor is bounced to login', async ({
  page,
  browser,
  freshUser,
}) => {
  const { owner, handle } = await publishedOwner(freshUser, stamp)
  await loginAs(page, owner.name)

  await page.goto('/collection')
  await page.getByRole('tab', { name: 'Shelves' }).click()
  const shelfManager = page.getByRole('region', { name: 'Manage shelves' })
  const backlogRow = shelfManager.getByRole('listitem').filter({ hasText: 'Backlog' })

  await backlogRow.getByRole('button', { name: 'Private' }).click()
  await expect(backlogRow.getByRole('button', { name: 'Copy link' })).toHaveCount(0)

  await page.goto('/explore')
  const explore = page.getByRole('main', { name: 'Explore' })
  const ownerCard = explore.getByRole('listitem').filter({ hasText: 'Backlog' }).filter({ hasText: handle })
  await expect(ownerCard).toHaveCount(0)

  await page.goto('/collection')
  await page.getByRole('tab', { name: 'Shelves' }).click()
  await backlogRow.getByRole('button', { name: 'Listed', exact: true }).click()
  await expect(backlogRow.getByRole('button', { name: 'Copy link' })).toBeVisible()

  await page.goto('/explore')
  await expect(ownerCard).toBeVisible()

  // browser.newContext doesn't inherit config's baseURL; pass it explicitly.
  const shelfPath = `/u/${handle}/shelves/Backlog`
  const loggedOutContext = await browser.newContext({ baseURL: BASE_URL, storageState: { cookies: [], origins: [] } })
  const loggedOutPage = await loggedOutContext.newPage()
  await loggedOutPage.goto(shelfPath)
  await expect(loggedOutPage).toHaveURL(`${BASE_URL}/login?next=${encodeURIComponent(shelfPath)}`)
  await loggedOutContext.close()
})

test('handle change cooldown', async ({ page, freshUser }) => {
  const user = await freshUser()
  await loginAs(page, user.name)
  await page.goto('/account')
  const handleInput = page.getByLabel('Handle')

  await handleInput.fill(`Cooldown1_${stamp}`)
  await page.getByRole('button', { name: 'Save', exact: true }).click()
  await expect(page.getByText('Saved.')).toBeVisible()

  // This 429 (handle_cooldown) is the one 429 a full run's gateway metrics are expected to show.
  await handleInput.fill(`Cooldown2_${stamp}`)
  await page.getByRole('button', { name: 'Save', exact: true }).click()
  await expect(page.getByRole('alert')).toHaveText('Handle changed too recently - try again later.')

  // HANDLE_CHANGE_COOLDOWN is a 5s dev-only override; test ends on the
  // rejection, throwaway identity needs no further proof.
})

test('an unlisted profile resolves by direct link but stays out of search', async ({ page, freshUser }) => {
  const { owner, handle } = await publishedOwner(freshUser, stamp)
  // Downgrade to unlisted: shelf stays reachable by link, profile drops out of search.
  await setProfile(owner.api, { profile_visibility: 'unlisted' })
  const viewer = await freshUser()
  await loginAs(page, viewer.name)

  await page.goto(`/u/${handle}`)
  await expect(page.getByRole('heading', { name: `@${handle}` })).toBeVisible()

  await page.goto('/explore')
  await page.getByRole('searchbox', { name: 'Search for people' }).fill(handle)
  const results = page.getByRole('list', { name: 'Search results' })
  await expect(results.getByRole('link', { name: `@${handle}` })).toHaveCount(0)
})
