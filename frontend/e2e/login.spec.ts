import { expect, test } from '@playwright/test'

test('dev fixture login round-trip through the gateway', async ({ page }) => {
  await page.goto('/')
  await expect(page).toHaveURL(/\/login$/)

  await page.getByRole('link', { name: 'alice', exact: true }).click()
  await expect(page).toHaveURL(/\/feed$/)
  await expect(page.getByText(/alice/i).first()).toBeVisible()
  await expect(page.getByRole('navigation', { name: 'Primary' })).toBeVisible()

  // The session cookie is HttpOnly: invisible to script.
  expect(await page.evaluate(() => document.cookie)).not.toContain('vg_session')

  await page.getByRole('button', { name: 'Log out' }).click()
  await expect(page).toHaveURL(/\/login$/)

  // Protected pages bounce without a session.
  await page.goto('/')
  await expect(page).toHaveURL(/\/login$/)
})
