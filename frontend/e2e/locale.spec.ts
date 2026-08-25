import { expect, test } from './fixtures'

// Asserts signed-in chrome translating (switcher works logged out too).
// Fresh context has no stored locale; config pins the browser to
// en-US, so every run begins in English.

test('the language switcher translates the app and the choice survives a reload', async ({
  page,
}) => {
  await page.goto('/')
  const html = page.locator('html')
  await expect(html).toHaveAttribute('lang', 'en')

  // Switching pulls the ja catalog chunk, re-renders nav text, and
  // retags the document lang.
  await page.getByRole('combobox', { name: 'Language' }).selectOption('ja')
  const nav = page.getByRole('navigation', { name: 'メイン' })
  await expect(nav).toBeVisible()
  await expect(nav.getByRole('link', { name: 'コレクション' })).toBeVisible()
  await expect(html).toHaveAttribute('lang', 'ja')

  // Choice is device-local (localStorage), not a profile field; reload stays in Japanese.
  await page.reload()
  await expect(page.getByRole('navigation', { name: 'メイン' })).toBeVisible()
  await expect(html).toHaveAttribute('lang', 'ja')

  // Switcher's label is in the active locale; find it under its
  // Japanese name to switch back.
  await page.getByRole('combobox', { name: '言語' }).selectOption('en')
  await expect(page.getByRole('navigation', { name: 'Primary' })).toBeVisible()
  await expect(html).toHaveAttribute('lang', 'en')
})
