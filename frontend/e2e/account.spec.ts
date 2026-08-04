import { expect, test, type Page } from '@playwright/test'

// One serial journey: the scenarios share dev-fixture identity state
// (who owns dev-bob changes mid-test), so ordering is the test.
// The journey is self-restoring: it ends by deleting bob's account,
// which unbinds dev-bob for the next run.

// The gateway rate-limits /api/auth/* to 20 requests per 60s per IP.
// Logins here take the programmatic 1-request form, but this journey
// still drives many auth navigations - the account-link clicks and the
// logouts each redirect through /login or /account, which refetches
// /api/auth/providers (a full-page auth redirect wipes the react-query
// cache) - so the auth request count still bursts. Pace it by counting
// real /api/auth/* requests and waiting before an action would breach a
// safe budget; the margin below 20 absorbs those refetch bursts.
const AUTH_BUDGET = 15
const authHits: number[] = []

async function throttleAuth(page: Page) {
  for (;;) {
    const cutoff = Date.now() - 60_000
    const inWindow = authHits.filter((t) => t > cutoff)
    if (inWindow.length < AUTH_BUDGET) return
    const waitMs = inWindow[0] + 60_000 - Date.now() + 750
    await page.waitForTimeout(Math.max(waitMs, 1000))
  }
}

// Programmatic dev-provider login: one GET seals the session cookie and
// redirects home, a single /api/auth/* hit (the old /login UI helper cost
// two). throttleAuth still gates it - the account-link clicks below are
// UI auth navigations that also hit /api/auth/*, so the budget still
// earns its keep - and the 1-request login leaves it more headroom.
async function login(page: Page, user: string) {
  await throttleAuth(page)
  await page.goto(`/api/auth/login?provider=dev&user=${user}`)
  await expect(page).toHaveURL(/\/feed$/)
}

async function logout(page: Page) {
  await throttleAuth(page)
  await page.getByRole('button', { name: 'Log out' }).click()
  await expect(page).toHaveURL(/\/login/)
}

async function openAccount(page: Page) {
  await throttleAuth(page)
  await page.getByRole('link', { name: 'Account' }).click()
  await expect(page).toHaveURL(/\/account$/)
}

test('profile edits, login linking, conflicts, and account deletion', async ({ page }) => {
  test.setTimeout(420_000)
  authHits.length = 0
  page.on('request', (req) => {
    if (req.url().includes('/api/auth/')) authHits.push(Date.now())
  })

  // Determinism: unbind dev-bob before the linking section. A login can
  // only land in its own account or a fresh one, never someone else's,
  // so deleting bob's account here leaves dev-bob unbound and alice's
  // link below succeeds instead of conflicting. The journey's final
  // section deletes bob again, so every later run starts here the same.
  await login(page, 'bob')
  await openAccount(page)
  await throttleAuth(page)
  page.once('dialog', (d) => void d.accept())
  await page.getByRole('button', { name: 'Delete account' }).click()
  await expect(page).toHaveURL(/\/login\?deleted=1$/)

  // Profile edit survives a re-login: provider claims fill only at creation.
  await login(page, 'alice')
  await openAccount(page)
  await page.getByLabel('Handle').fill('Alice_Prime')
  await page.getByRole('button', { name: 'Save' }).click()
  await expect(page.getByText('Saved.')).toBeVisible()
  await logout(page)
  await login(page, 'alice')
  await expect(page.getByText('@Alice_Prime')).toBeVisible()

  // Link dev-bob to alice's account; bob's login now lands there.
  await openAccount(page)
  await throttleAuth(page)
  await page.getByRole('link', { name: 'bob', exact: true }).click()
  await expect(page).toHaveURL(/\/account\?linked=dev$/)
  await expect(page.getByText('bob@example.com')).toBeVisible()
  await logout(page)
  await login(page, 'bob')
  await expect(page.getByText('@Alice_Prime')).toBeVisible()

  // Unlink bob (from the shared account); bob then gets his own account.
  await openAccount(page)
  page.once('dialog', (d) => void d.accept())
  await page
    .getByRole('listitem')
    .filter({ hasText: 'bob@example.com' })
    .getByRole('button', { name: 'Unlink' })
    .click()
  await expect(page.getByText('bob@example.com')).toBeHidden()
  await logout(page)
  await login(page, 'bob')
  await expect(page.getByText('@Bob_Fixture')).toBeVisible()

  // Conflict: dev-alice already belongs to alice's account.
  await openAccount(page)
  await throttleAuth(page)
  await page.getByRole('link', { name: 'alice', exact: true }).click()
  await expect(page).toHaveURL(/\/account\?link_error=conflict$/)
  await expect(page.getByRole('alert')).toContainText(/already belongs/i)

  // Deletion: bob's account goes away; logging in again starts fresh.
  await throttleAuth(page)
  page.once('dialog', (d) => void d.accept())
  await page.getByRole('button', { name: 'Delete account' }).click()
  await expect(page).toHaveURL(/\/login\?deleted=1$/)
  await expect(page.getByRole('status')).toContainText(/deleted/i)
  await login(page, 'bob')
  await expect(page.getByText('@Bob_Fixture')).toBeVisible()
})
