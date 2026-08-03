import { expect, test, type Page } from '@playwright/test'

// Runs against the live dev stack through the gateway port-forward
// (task run, then task e2e). The switcher sits in the footer, which
// renders with or without a session; what this spec asserts is the
// signed-in chrome translating, so it logs in once via the dev
// provider (one /api/auth hit; the gateway caps that bucket at 20/min
// per IP). A fresh context starts with no stored locale and the config
// pins the browser to en-US, so every run begins in English.
async function login(page: Page) {
  await page.goto('/api/auth/login?provider=dev&user=alice')
  await expect(page.getByRole('navigation', { name: 'Primary' })).toBeVisible()
}

test('the language switcher translates the app and the choice survives a reload', async ({
  page,
}) => {
  await login(page)
  const html = page.locator('html')
  await expect(html).toHaveAttribute('lang', 'en')

  // Switching pulls the ja catalog chunk, re-renders the chrome from
  // it - the nav's accessible name and its link text both come from
  // the catalog - and retags the document so assistive tech reads the
  // page with Japanese pronunciation rules.
  await page.getByRole('combobox', { name: 'Language' }).selectOption('ja')
  const nav = page.getByRole('navigation', { name: 'メイン' })
  await expect(nav).toBeVisible()
  await expect(nav.getByRole('link', { name: 'コレクション' })).toBeVisible()
  await expect(html).toHaveAttribute('lang', 'ja')

  // The choice is device-local (localStorage), not a profile field, so
  // reloading the same context boots straight back into Japanese.
  await page.reload()
  await expect(page.getByRole('navigation', { name: 'メイン' })).toBeVisible()
  await expect(html).toHaveAttribute('lang', 'ja')

  // The switcher's own label is rendered in the active locale, so
  // getting back to English means finding it under its Japanese name.
  await page.getByRole('combobox', { name: '言語' }).selectOption('en')
  await expect(page.getByRole('navigation', { name: 'Primary' })).toBeVisible()
  await expect(html).toHaveAttribute('lang', 'en')
})
