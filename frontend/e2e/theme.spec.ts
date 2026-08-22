import { expect, test } from './fixtures'

// The browser reports a dark system preference so the dark default is
// assertable (Playwright's own default is light). This runs against
// the bff's CSP, which blocks inline scripts - exactly what a unit
// test cannot see.
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
