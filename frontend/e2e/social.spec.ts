import type { APIRequestContext } from '@playwright/test'
import { expect, loginAs, test, BASE_URL, type TestUser } from './fixtures'
import { createEntry, listViews, setProfile, setViewVisibility } from './seed'

// Four tests covering the social surface. Two of them are flows that
// switch between an owner identity and a viewer identity within the
// same test - publish, discover, follow, like, and comment in one
// continuous flow, then a separate visibility-and-guard flow - because
// the gateway budgets /api/auth/* per IP per minute (120 on this dev
// stack; production keeps it far tighter), shared across every test
// in a run. Identity switching is not incidental overhead here: it is
// the behavior under test (an owner acts, a viewer discovers and
// engages, the owner reads the result), so folding the related steps
// into one flow is truer to the real thing than re-minting a fresh
// owner/viewer pair per assertion - and it is also what keeps this
// file's total auth-route hits inside the shared budget. The other
// two tests, the handle-change cooldown and the unlisted-profile
// edge, stay single-identity and independent; neither needs a second
// actor. Every test still starts from a private, social-empty
// account, so counts asserted below are exact, never deltas against
// ambient activity.
test.use({ storageState: { cookies: [], origins: [] } })

const stamp = Date.now().toString(36)

// The Backlog view every account is seeded with the first time its
// views are listed. Shared by publishedOwner and the flow that
// publishes through the UI instead, so the id/name/params lookup
// exists in one place.
async function backlogView(api: APIRequestContext) {
  const views = await listViews(api)
  const backlog = views.find((v) => v.name === 'Backlog')
  if (!backlog) throw new Error('Backlog view not found')
  return backlog
}

// Publishes a listed profile and a listed Backlog shelf with one
// stamped item, all through the API. The flow that owns the UI publish
// itself drives that through its own clicks instead of this helper;
// every other test that needs an already-published owner starts from
// this state instead of repeating the UI steps.
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
  // One stamped backlog item, created via the API so the discovery leg
  // below has a numbered row to prove against - the publish steps
  // themselves only prove visibility, not content.
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

  // The comment legs below need the shelf id for their response-wait
  // URL match; fetched directly since this flow publishes through the
  // UI instead of the publishedOwner helper.
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

  // Shared reads never link into /entries/ (the viewer does not own
  // these rows), and Backlog sorts by rank, so the read renders a
  // numbered rank column instead.
  const entriesRegion = page.getByRole('region', { name: 'Entries' })
  await expect(entriesRegion.locator('a[href*="/entries/"]')).toHaveCount(0)
  await expect(entriesRegion.getByRole('columnheader', { name: '#' })).toBeVisible()
  const itemRow = entriesRegion.getByRole('row', { name: new RegExp(`Social Item ${stamp}`) })
  await expect(itemRow.getByRole('cell').first()).toHaveText(/^\d+$/)

  // A freshly minted owner carries zero ambient likes, so the count
  // after this one click is exact, not a delta off some prior total.
  const likeBtn = page.getByRole('button', { name: 'Like', exact: true })
  await likeBtn.click()
  const unlike = page.getByRole('button', { name: 'Unlike' })
  await expect(unlike).toBeVisible()
  await expect(unlike).toHaveText(/^\u2665\s*1$/)

  // Timers created after clock.install() are controllable; installing
  // it here, mid-flow and right before the comment legs, is fine
  // because the timer this test needs to control - the undo window in
  // frontend/src/components/social/useCommentDelete.ts - is not
  // created until the first Delete click, well below. Fast-forwarding
  // past it later replaces waiting out the real 7-second window.
  await page.clock.install()

  const commentText = `e2e comment ${stamp}`
  await page.getByLabel('Add a comment').fill(commentText)
  // Armed before the click, so the response can never arrive
  // unobserved - a response wait, not a sleep.
  const commentPost = page.waitForResponse(
    (r) => r.url().includes(`/api/shelves/${shelfId}/comments`) && r.request().method() === 'POST' && r.status() === 201,
  )
  await page.getByRole('button', { name: 'Post', exact: true }).click()
  const posted = (await (await commentPost).json()) as { id: string }
  expect(posted.id.length).toBeGreaterThan(0)
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
  await page.clock.fastForward(8_500)
  await expect(page.getByText(commentText)).toHaveCount(0)
  await expect(page.getByRole('status')).toHaveCount(0)

  // The viewer already follows the owner and liked this shelf from the
  // legs above, and the delete just committed a real, dead comment -
  // both feeds below need no further arranging.
  await page.goto('/feed')
  const viewerFeed = page.getByRole('main', { name: 'Feed' })
  // Following is the default tab: the viewer follows only the owner,
  // so the owner's publish is expected without switching tabs.
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

  // browser.newContext does not inherit the config's baseURL, so the
  // second, unauthenticated context gets it passed explicitly.
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

  // The rejected save below answers 429 handle_cooldown (an
  // application response relayed through the gateway), which makes it
  // the one 429 a full run's gateway metrics are expected to show.
  await handleInput.fill(`Cooldown2_${stamp}`)
  await page.getByRole('button', { name: 'Save', exact: true }).click()
  await expect(page.getByRole('alert')).toHaveText('Handle changed too recently - try again later.')

  // The live user pod's HANDLE_CHANGE_COOLDOWN is a 5-second dev-only
  // override, not the production cooldown; this test ends on the
  // rejected second change instead of waiting the override out and
  // retrying - the identity is a throwaway with nothing further to
  // prove or restore.
})

test('an unlisted profile resolves by direct link but stays out of search', async ({ page, freshUser }) => {
  const { owner, handle } = await publishedOwner(freshUser, stamp)
  // publishedOwner leaves the profile listed; downgrade to unlisted
  // here so the shelf stays reachable by link while the profile itself
  // drops out of search.
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
