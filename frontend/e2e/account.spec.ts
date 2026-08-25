import { acceptNext, expect, loginAs, logout, test } from './fixtures'
import { setProfile } from './seed'

// Each test mints its own freshUser identities, isolated by construction.
// Linking folds link/unlink/conflict into one test to save auth hits.
// Gateway budgets /api/auth/* to 240/IP/min (dev); this file opts out of
// the shared worker session and runs serially so hits land in one window.
test.use({ storageState: { cookies: [], origins: [] } })
test.describe.configure({ mode: 'default' })

const stamp = Date.now().toString(36)

test('profile edits persist across re-login', async ({ page, freshUser }) => {
  const a = await freshUser()
  const handle = `A_${stamp}`

  await loginAs(page, a.name)
  await page.getByRole('link', { name: 'Account', exact: true }).click()
  // Pins exactly one PATCH per Save click; catches a duplicate-fire
  // regression here instead of in access logs.
  let patches = 0
  page.on('request', (r) => {
    if (r.method() === 'PATCH' && new URL(r.url()).pathname === '/api/me') patches += 1
  })
  await page.getByLabel('Handle').fill(handle)
  await page.getByRole('button', { name: 'Save' }).click()
  await expect(page.getByText('Saved.')).toBeVisible()
  expect(patches, 'one Save click sends exactly one PATCH /api/me').toBe(1)

  // Provider claims only fill the profile at creation; the edit must
  // come from storage.
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
  // freshUser's login provisions b's own account; drop it first since
  // linking requires a free login.
  const dropB = await b.api.delete('/api/me')
  expect(dropB.ok(), `delete ${b.name}: ${dropB.status()}`).toBeTruthy()

  await loginAs(page, a.name)
  // Link buttons render only for the fixed alice/bob/admin trio; a
  // minted identity uses the same URL.
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

  // Unlinked, b's login provisions a new account instead of landing back in a's.
  await logout(page)
  await loginAs(page, b.name)
  await expect(page.getByText(`@${aHandle}`)).not.toBeVisible()

  // a's login stays bound to a's own account, so linking it here
  // conflicts (same rule that required unbinding b above).
  await page.goto(`/api/auth/link?provider=dev&user=${a.name}`)
  await expect(page).toHaveURL(/\/account\?link_error=conflict$/)
  await expect(page.getByRole('alert')).toContainText(/already belongs/i)
})

test('account deletion round trip', async ({ page, freshUser }) => {
  const c = await freshUser()
  // Stamps a handle first so the post-delete default is observably different.
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
