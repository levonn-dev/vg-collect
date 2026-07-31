import { expect, test, type Page } from '@playwright/test'

// Runs against the live dev stack through the gateway port-forward
// (task run, then task e2e). The public pages need no session; the
// help leg logs in once via the dev provider (one /api/auth hit;
// the gateway caps that bucket at 20/min per IP).
// Credit lines are not asserted here on purpose: their presence
// varies with the operator's .env, and unit tests own that logic.
async function login(page: Page) {
  await page.goto('/api/auth/login?provider=dev&user=alice')
  await expect(page.getByRole('navigation', { name: 'Primary' })).toBeVisible()
}

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

test('help requires login, then renders the shelves walkthrough', async ({ page }) => {
  await page.goto('/help')
  await expect(page).toHaveURL(/\/login\?next=%2Fhelp/)
  await login(page)
  await page.goto('/help')
  await expect(page.getByRole('heading', { name: 'Shelves from tags' })).toBeVisible()
})
