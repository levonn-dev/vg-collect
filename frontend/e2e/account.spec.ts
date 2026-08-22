import { acceptNext, expect, loginAs, logout, test } from './fixtures'
import { setProfile } from './seed'

// Three independent tests covering the self-service account surface:
// a profile edit surviving re-login, one linking journey, and account
// deletion. Each test mints exactly the identities it needs and never
// shares state with another test - the old throttleAuth machinery,
// its authHits counter, and the delete-bob-first determinism dance
// are gone along with the fixed alice/bob accounts they existed to
// protect; a freshUser is unbound by construction, so no test here
// has anything to steal from another. UI logout coverage already
// lives in login.spec.ts's own round trip; nothing here repeats it.
//
// The linking journey folds what would otherwise be three tests (link,
// unlink, conflict) into one: each of those needs its own real login
// or two to observe, and this file has no budget left to spend minting
// and re-authenticating three separate scenarios on top of the profile
// and deletion tests below. It is this suite's one sanctioned
// exception to independent-per-behavior tests, made for that reason -
// see the journey test's own comments for why each step still needs
// the auth hit it spends.
//
// Every test here drives at least one full-page auth navigation, and
// the gateway budgets /api/auth/* per IP per minute (240 on this dev
// stack; production keeps it far tighter). The file opts out of the
// worker's shared session below (every test authenticates its own
// identities via loginAs, so the worker fixture's own login would
// spend budget for nothing) and runs its tests in declared order on a
// single worker rather than Playwright's default full parallelism, so
// the file's own auth hits land in one predictable, budget-sized
// window instead of several tests firing their bursts at once.
test.use({ storageState: { cookies: [], origins: [] } })
test.describe.configure({ mode: 'default' })

const stamp = Date.now().toString(36)

test('profile edits persist across re-login', async ({ page, freshUser }) => {
  const a = await freshUser()
  const handle = `A_${stamp}`

  await loginAs(page, a.name)
  await page.getByRole('link', { name: 'Account', exact: true }).click()
  // One Save click sends exactly one PATCH /api/me; the count is
  // pinned so a regression toward duplicate fires shows up here as a
  // failed assertion instead of as stray gateway traffic that someone
  // has to attribute from access logs.
  let patches = 0
  page.on('request', (r) => {
    if (r.method() === 'PATCH' && new URL(r.url()).pathname === '/api/me') patches += 1
  })
  await page.getByLabel('Handle').fill(handle)
  await page.getByRole('button', { name: 'Save' }).click()
  await expect(page.getByText('Saved.')).toBeVisible()
  expect(patches, 'one Save click sends exactly one PATCH /api/me').toBe(1)

  // Provider claims only fill the profile at account creation, so the
  // edit has to come back from storage, not from the dev provider.
  await logout(page)
  await loginAs(page, a.name)
  await expect(page.getByText(`@${handle}`)).toBeVisible()
})

test('linking moves a login into the account, unlink restores it, and a bound login conflicts', async ({
  page,
  freshUser,
}) => {
  const a = await freshUser()
  const b = await freshUser()
  const aHandle = `A2_${stamp}`
  await setProfile(a.api, { handle: aHandle })
  // freshUser mints b by logging it in once, which provisions b's own
  // account as a side effect; linking requires the login to be free,
  // so drop that account before it is used as a login to link.
  const dropB = await b.api.delete('/api/me')
  expect(dropB.ok(), `delete ${b.name}: ${dropB.status()}`).toBeTruthy()

  await loginAs(page, a.name)
  // The Account page renders a link button only for the fixed
  // alice/bob/admin trio; a minted identity links through the same
  // URL those buttons carry.
  await page.goto(`/api/auth/link?provider=dev&user=${b.name}`)
  await expect(page).toHaveURL(/\/account\?linked=dev$/)
  await expect(page.getByText(`${b.name}@example.com`)).toBeVisible()

  // b's login now lands in a's account.
  await logout(page)
  await loginAs(page, b.name)
  await expect(page.getByText(`@${aHandle}`)).toBeVisible()

  await page.getByRole('link', { name: 'Account', exact: true }).click()
  acceptNext(page)
  await page
    .getByRole('listitem')
    .filter({ hasText: `${b.name}@example.com` })
    .getByRole('button', { name: 'Unlink' })
    .click()
  await expect(page.getByText(`${b.name}@example.com`)).toBeHidden()

  // Unlinked, b's login provisions its own account again instead of
  // landing back in a's.
  await logout(page)
  await loginAs(page, b.name)
  await expect(page.getByText(`@${aHandle}`)).not.toBeVisible()

  // Still signed in as b, no switch back to a: a's login is bound to
  // a's own account (freshUser minted it the same way b's was, before
  // b got unbound above), so linking it here conflicts instead of
  // moving it - the same rule that made unbinding b necessary above,
  // seen from the other side, and three fewer auth hits than standing
  // this scenario up as its own test would cost.
  await page.goto(`/api/auth/link?provider=dev&user=${a.name}`)
  await expect(page).toHaveURL(/\/account\?link_error=conflict$/)
  await expect(page.getByRole('alert')).toContainText(/already belongs/i)
})

test('account deletion round trip', async ({ page, freshUser }) => {
  const c = await freshUser()
  // Stamp a handle before deleting so the reset is observable: a
  // fresh account's default handle only proves the reset happened if
  // it differs from something known to have been there before.
  const handle = `C_${stamp}`
  await setProfile(c.api, { handle })

  await loginAs(page, c.name)
  await page.getByRole('link', { name: 'Account', exact: true }).click()
  acceptNext(page)
  await page.getByRole('button', { name: 'Delete account' }).click()
  await expect(page).toHaveURL(/\/login\?deleted=1$/)
  await expect(page.getByRole('status')).toContainText(/deleted/i)

  await loginAs(page, c.name)
  await expect(page.getByText(`@${handle}`)).not.toBeVisible()
})
