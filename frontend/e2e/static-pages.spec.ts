import { expect, loginAs, test } from './fixtures'

// Public pages need no session; opts out of the worker's storageState.
// help logs in separately (240/min/IP dev cap). Credit lines aren't
// asserted here: presence varies by operator .env, unit tests own that.
test.use({ storageState: { cookies: [], origins: [] } })

test('about, terms, and privacy render logged out with the footer', async ({ page }) => {
  await page.goto('/about')
  await expect(page.getByRole('heading', { name: /^About / })).toBeVisible()
  const footer = page.getByRole('contentinfo')
  await expect(footer.getByRole('link', { name: 'Terms' })).toBeVisible()
  await expect(footer.getByRole('link', { name: 'Source' })).toBeVisible()
  await page.goto('/terms')
  await expect(page.getByRole('heading', { name: 'Terms of service' })).toBeVisible()
  await page.goto('/privacy')
  await expect(page.getByRole('heading', { name: 'Privacy policy' })).toBeVisible()
})

test('an unknown path renders the not-found page', async ({ page }) => {
  await page.goto('/no-such-page')
  await expect(page.getByRole('heading', { name: 'Page not found' })).toBeVisible()
})

test('help requires login, then renders the shelves walkthrough', async ({ page, user }) => {
  await page.goto('/help')
  await expect(page).toHaveURL(/\/login\?next=%2Fhelp/)
  await loginAs(page, user.name)
  await page.goto('/help')
  await expect(page.getByRole('heading', { name: 'Shelves from tags' })).toBeVisible()
})
