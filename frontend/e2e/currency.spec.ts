import { expect, test, type Page } from '@playwright/test'

// One serial journey as the dev fixture alice. First, in USD, it proves
// the library summary sums a custom price and a proxy price alike. Then
// it flips the display currency to EUR, prices a throwaway custom entry,
// verifies market conversion and the typed-price pin both ways, and
// finally deletes the entry and restores USD - so the other specs' USD
// assertions still hold and the stack stays re-runnable. Every created
// entry is stamped unique and deleted, so a run never collides with
// residue from earlier runs.
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

// Parse a currency string like "$1,234.56" into integer cents.
function toCents(text: string): number {
  return Math.round(Number(text.replace(/[^0-9.]/g, '')) * 100)
}

// Create a backlog custom (off-catalog) entry and land on its page. This
// mirrors the custom-creation flow the pricing specs use (Add -> custom
// -> name/platform -> details defaults backlog -> confirm).
async function addCustomEntry(page: Page, name: string) {
  await page.getByRole('link', { name: 'Add', exact: true }).click()
  await page.getByRole('button', { name: /add it as a custom item/i }).click()
  await page.getByLabel('Name', { exact: true }).fill(name)
  await page.getByLabel('Platform', { exact: true }).fill('SNES')
  await page.getByRole('button', { name: 'Continue' }).click()
  await page.getByRole('button', { name: 'Continue' }).click() // details defaults: backlog
  await expect(page.getByText(/start without market pricing/i)).toBeVisible()
  await page.getByRole('button', { name: 'Add to collection' }).click()
  await expect(page.getByRole('heading', { name })).toBeVisible()
  await expect(page).toHaveURL(/\/entries\//)
}

test('display currency converts market values and pins the typed custom price', async ({ page }) => {
  test.setTimeout(180_000)
  await login(page)

  const selector = page.getByLabel('Display currency')
  await expect(selector).toBeEnabled()
  const pricing = page.getByRole('region', { name: 'Pricing' })

  // The library summary sums every priced entry alike - a user-set custom
  // price and a proxied market listing both feed it. Do this first, on the
  // freshly loaded dashboard, in USD: with no conversion each shown price
  // equals its own contribution, so the total must climb by exactly the
  // two. Both created entries are removed again here.
  await selector.selectOption('USD')
  await expect(selector).toHaveValue('USD')
  const valueCard = page
    .getByRole('region', { name: 'Totals' })
    .locator('div')
    .filter({ hasText: 'Collection value' })
  const totalValue = valueCard.getByText(/^\$[\d,]+\.\d{2}$/)
  const pricedNow = async (): Promise<number> => {
    const line = (await valueCard.getByText(/unpriced/).textContent()) ?? ''
    return Number(line.match(/(\d+) priced/)?.[1] ?? 'NaN')
  }
  await expect(totalValue).toBeVisible()
  const baseTotal = toCents((await totalValue.textContent()) ?? '')
  const basePriced = await pricedNow()

  // A custom-priced entry: the user's own number becomes its value.
  await addCustomEntry(page, `Summed Custom ${Date.now()}`)
  const customURL = page.url()
  await page.getByRole('radio', { name: /custom/i }).click()
  await page.getByLabel(/custom price \(usd\)/i).fill('42.50')
  await page.getByRole('button', { name: /save changes/i }).click()
  await expect(page.getByText('Saved.')).toBeVisible()
  const customLine = pricing.locator('p').first()
  await expect(customLine).toHaveText(/^\$[\d,]+\.\d{2}$/)
  const customShown = toCents((await customLine.textContent()) ?? '')
  expect(customShown).toBe(4250)

  // A proxy-priced entry: a real listing prices this copy.
  await addCustomEntry(page, `Summed Proxy ${Date.now()}`)
  const proxyURL = page.url()
  await page.getByRole('radio', { name: /proxy/i }).click()
  const summedPicker = page.getByRole('dialog', { name: /choose a price source/i })
  await expect(summedPicker).toBeVisible()
  await summedPicker
    .getByRole('searchbox', { name: /search for games, hardware, and pricecharting/i })
    .fill('chrono trigger')
  await summedPicker.getByRole('button', { name: 'Search', exact: true }).click()
  await summedPicker
    .getByRole('button', { name: /Chrono Trigger on Super Nintendo Entertainment System/ })
    .first()
    .click()
  await expect(page.getByText('Price source:')).toBeVisible()
  await page.getByRole('button', { name: /save changes/i }).click()
  await expect(page.getByText('Saved.')).toBeVisible()
  const summedProxyLine = pricing.locator('p').first()
  await expect(summedProxyLine).toHaveText(/^\$[\d,]+\.\d{2}$/)
  const proxyShown = toCents((await summedProxyLine.textContent()) ?? '')
  expect(proxyShown).toBeGreaterThan(0)

  // Back on the dashboard through the nav (SPA, no auth refetch): once
  // both have priced, the total has climbed by exactly the two shown
  // values.
  await page.getByRole('link', { name: 'Collection', exact: true }).click()
  await expect(page).toHaveURL(/\/$/)
  await expect(valueCard.getByText(new RegExp(`\\b${basePriced + 2} priced\\b`))).toBeVisible()
  expect(toCents((await totalValue.textContent()) ?? '') - baseTotal).toBe(customShown + proxyShown)

  // Remove both; the total and priced count return to baseline.
  for (const url of [customURL, proxyURL]) {
    await page.goto(url)
    acceptNext(page)
    await page.getByRole('button', { name: 'Delete entry' }).click()
    await expect(page).toHaveURL(/\/$/)
  }
  await expect(valueCard.getByText(new RegExp(`\\b${basePriced} priced\\b`))).toBeVisible()
  expect(toCents((await totalValue.textContent()) ?? '')).toBe(baseTotal)

  // Switch the profile to EUR from the header. The choice is optimistic,
  // so every price re-renders at once without a reload.
  await selector.selectOption('EUR')
  await expect(selector).toHaveValue('EUR')

  // The dashboard total re-labels in euros.
  await expect(page.getByText('Collection value (EUR)')).toBeVisible()

  // Create a custom (off-catalog) entry to price.
  await addCustomEntry(page, entryName)

  // The paid-price field is stamped in EUR (the profile currency at
  // create), independent of whatever the display currency later becomes.
  await expect(page.getByText(/price paid \(eur\)/i)).toBeVisible()

  // Custom price typed in euros: saved, then redisplayed verbatim. The
  // pin rule renders the entered pair exactly as typed while EUR is the
  // display currency, and the non-USD note explains the conversion.
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

  // A market quote is not pinned: it always converts. Point the same
  // entry at a real listing so it carries a market value, read that
  // value in USD, then flip to EUR and assert the exact conversion at
  // the one snapshot rate fetched above.
  await page.getByRole('radio', { name: /proxy/i }).click()
  const picker = page.getByRole('dialog', { name: /choose a price source/i })
  await expect(picker).toBeVisible()
  await picker
    .getByRole('searchbox', { name: /search for games, hardware, and pricecharting/i })
    .fill('chrono trigger')
  await picker.getByRole('button', { name: 'Search', exact: true }).click()
  await picker
    .getByRole('button', { name: /Chrono Trigger on Super Nintendo Entertainment System/ })
    .first()
    .click()
  await expect(page.getByText('Price source:')).toBeVisible()
  await page.getByRole('button', { name: /save changes/i }).click()
  await expect(page.getByText('Saved.')).toBeVisible()

  // The market value is the panel's headline figure (scoped to the first
  // line so the leftover "Last custom price" note cannot stand in). In
  // USD it prints the snapshot verbatim; capture and parse it to cents.
  const marketLine = pricing.locator('p').first()
  await selector.selectOption('USD')
  await expect(pricing.getByText('Market values are in USD.')).toBeVisible()
  await expect(marketLine).toHaveText(/^\$[\d,]+\.\d{2}$/)
  const marketUsdCents = toCents((await marketLine.textContent()) ?? '')
  expect(marketUsdCents).toBeGreaterThan(0)

  // Flip to EUR: that same figure must convert at `rate`, formatted the
  // way the browser formats it under the pinned locale.
  const marketEur = new Intl.NumberFormat('en-US', { style: 'currency', currency: 'EUR' }).format(
    (marketUsdCents / 100) * rate,
  )
  await selector.selectOption('EUR')
  await expect(pricing.getByText(/converted from usd at ecb rates/i)).toBeVisible()
  await expect(marketLine).toHaveText(marketEur)

  // Clean up: delete the entry, then restore USD. toBeEnabled waits for
  // the currency PUT to settle (the select disables while saving), and
  // toHaveValue confirms the write took, so alice is persisted back to
  // USD before the test ends.
  acceptNext(page)
  await page.getByRole('button', { name: 'Delete entry' }).click()
  await expect(page).toHaveURL(/\/$/)
  await selector.selectOption('USD')
  await expect(page.getByText('Collection value (USD)')).toBeVisible()
  await expect(selector).toBeEnabled()
  await expect(selector).toHaveValue('USD')
})
