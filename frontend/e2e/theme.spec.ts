import { expect, test } from './fixtures'

// Dark system preference makes the dark default assertable (Playwright
// defaults light); runs against the bff's real CSP, which blocks
// inline scripts a unit test can't see.
test.use({ colorScheme: 'dark' })

test('dark by default under a dark system preference; explicit choice survives reload', async ({ page }) => {
  await page.goto('/')
  await expect(page.locator('html')).toHaveClass(/dark/)
  await page.getByRole('button', { name: 'Switch to light mode' }).click()
  await page.reload()
  await expect(page.locator('html')).not.toHaveClass(/dark/)
  await page.getByRole('button', { name: 'Switch to dark mode' }).click()
  await expect(page.locator('html')).toHaveClass(/dark/)
})
