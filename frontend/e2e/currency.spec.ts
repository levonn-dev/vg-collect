import { expect, test, type Page } from '@playwright/test'

// One serial journey as the dev fixture alice. It flips alice's display
// currency to EUR, prices a throwaway custom entry, verifies the market
// conversion and the typed-price pin both ways, then deletes the entry
// and restores USD - so the other specs' USD assertions still hold and
// the stack stays re-runnable. The paired name is stamped unique and
// scoped so it never collides with residue from earlier runs.
const entryName = `Currency Journey ${Date.now()}`

async function login(page: Page) {
  await page.goto('/login')
  await page.getByRole('link', { name: 'alice', exact: true }).click()
  await expect(page.getByRole('navigation', { name: 'Primary' })).toBeVisible()
}

// Accept the next native dialog (the delete confirm) once.
function acceptNext(page: Page) {
  page.once('dialog', (d) => void d.accept())
}

test('display currency converts market values and pins the typed custom price', async ({ page }) => {
  test.setTimeout(120_000)
  await login(page)

  // Switch the profile to EUR from the header. The choice is optimistic,
  // so every price re-renders at once without a reload.
  const selector = page.getByLabel('Display currency')
  await expect(selector).toBeEnabled()
  await selector.selectOption('EUR')
  await expect(selector).toHaveValue('EUR')

  // The dashboard total re-labels in euros.
  await expect(page.getByText('Collection value (EUR)')).toBeVisible()

  // Create a custom (off-catalog) entry to price. This mirrors the exact
  // custom-creation flow in collection-journey.spec.ts (Add -> custom ->
  // name/platform -> details defaults backlog -> confirm).
  await page.getByRole('link', { name: 'Add', exact: true }).click()
  await page.getByRole('button', { name: /add it as a custom item/i }).click()
  await page.getByLabel('Name', { exact: true }).fill(entryName)
  await page.getByLabel('Platform', { exact: true }).fill('SNES')
  await page.getByRole('button', { name: 'Continue' }).click()
  await page.getByRole('button', { name: 'Continue' }).click() // details defaults: backlog
  await expect(page.getByText(/start without market pricing/i)).toBeVisible()
  await page.getByRole('button', { name: 'Add to collection' }).click()
  await expect(page.getByRole('heading', { name: entryName })).toBeVisible()
  await expect(page).toHaveURL(/\/entries\//)

  // The paid-price field is stamped in EUR (the profile currency at
  // create), independent of whatever the display currency later becomes.
  await expect(page.getByText(/price paid \(eur\)/i)).toBeVisible()

  // Custom price typed in euros: saved, then redisplayed verbatim. The
  // pin rule renders the entered pair exactly as typed while EUR is the
  // display currency, and the non-USD note explains the conversion.
  const pricing = page.getByRole('region', { name: 'Pricing' })
  await page.getByRole('radio', { name: /custom/i }).click()
  await page.getByLabel(/custom price \(eur\)/i).fill('60')
  await page.getByRole('button', { name: /save changes/i }).click()
  await expect(page.getByText('Saved.')).toBeVisible()
  await expect(pricing.getByText(/\u20ac60\.00/)).toBeVisible()
  await expect(pricing.getByText(/converted from usd at ecb rates/i)).toBeVisible()

  // The USD snapshot equals round(6000 / rate) cents at whatever rate
  // the stack is serving (stub or real) - fetched with the session
  // cookie, so the journey stays exact without pinning a provider mode.
  const fx = await page.request.get('/api/fx')
  expect(fx.ok()).toBeTruthy()
  const rate = (await fx.json()).rates.EUR as number
  const usd = `$${(Math.round(6000 / rate) / 100).toFixed(2)}`

  // Back to USD: the pin no longer applies (entered currency != display),
  // so the USD snapshot shows.
  await selector.selectOption('USD')
  await expect(pricing.getByText(usd)).toBeVisible()
  await expect(pricing.getByText('Market values are in USD.')).toBeVisible()

  // Back to EUR: the typed number re-pins exactly.
  await selector.selectOption('EUR')
  await expect(pricing.getByText(/\u20ac60\.00/)).toBeVisible()

  // Clean up: delete the entry, then restore USD. toBeEnabled waits for
  // the currency PUT to settle (the select disables while saving), so
  // alice is persisted back to USD before the test ends.
  acceptNext(page)
  await page.getByRole('button', { name: 'Delete entry' }).click()
  await expect(page).toHaveURL(/\/$/)
  await selector.selectOption('USD')
  await expect(page.getByText('Collection value (USD)')).toBeVisible()
  await expect(selector).toBeEnabled()
})
