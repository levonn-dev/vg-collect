import { expect, loginAs, test } from './fixtures'
import { createEntry } from './seed'

// Every test mints its own freshUser: currency and totals are
// user-global, so shared entries would spoil the summary test's zero
// baseline. Teardown deletes the user and its entries automatically.
const stamp = `e2e-price-${Date.now()}`

// Parse a currency string like "$1,234.56" into integer cents.
function toCents(text: string): number {
  return Math.round(Number(text.replace(/[^0-9.]/g, '')) * 100)
}

test('proxy pricing prices a custom copy', async ({ page, freshUser }) => {
  const fresh = await freshUser()
  const name = `Proxy Target ${stamp}`
  const entry = await createEntry(fresh.api, { display_name: name })

  await loginAs(page, fresh.name)
  await page.goto(entry.url)

  // Selecting proxy flips the radio and opens the picker; nothing
  // reaches the server until saved.
  await page.getByRole('radio', { name: /proxy/i }).click()
  await expect(page.getByRole('radio', { name: /proxy/i })).toBeChecked()
  const picker = page.getByRole('dialog', { name: /choose a price source/i })
  await expect(picker).toBeVisible()
  const pickerSearch = picker.getByRole('searchbox', { name: /search for games, hardware, and pricecharting/i })
  // Picker prefills from the entry's title (no edition set) and auto-fires the search.
  await expect(pickerSearch).toHaveValue(name)
  await pickerSearch.fill('chrono trigger')
  await picker.getByRole('button', { name: 'Search', exact: true }).click()
  await picker.getByRole('button', { name: /Chrono Trigger on Super Nintendo Entertainment System/ }).first().click()
  await expect(page.getByText('Price source:')).toBeVisible()
  await page.getByRole('button', { name: 'Save changes' }).click()
  await expect(page.getByText('Saved.')).toBeVisible()
  // Copy value and match breakdown both show a dollar figure; take first().
  await expect(page.getByRole('region', { name: 'Pricing' }).getByText(/\$\d/).first()).toBeVisible()
})

test('custom price overrides the market source', async ({ page, freshUser }) => {
  const fresh = await freshUser()
  const entry = await createEntry(fresh.api, { display_name: `Custom Override ${stamp}` })

  await loginAs(page, fresh.name)
  await page.goto(entry.url)

  // Override below needs an active market source to override first.
  await page.getByRole('radio', { name: /proxy/i }).click()
  const picker = page.getByRole('dialog', { name: /choose a price source/i })
  await expect(picker).toBeVisible()
  await picker.getByRole('searchbox', { name: /search for games, hardware, and pricecharting/i }).fill('chrono trigger')
  await picker.getByRole('button', { name: 'Search', exact: true }).click()
  await picker.getByRole('button', { name: /Chrono Trigger on Super Nintendo Entertainment System/ }).first().click()
  await expect(page.getByText('Price source:')).toBeVisible()
  await page.getByRole('button', { name: 'Save changes' }).click()
  await expect(page.getByText('Saved.')).toBeVisible()

  // Custom price overrides the market source, packaging-independent.
  await page.getByRole('radio', { name: /custom/i }).click()
  await page.getByLabel(/custom price \(usd\)/i).fill('123.45')
  await page.getByRole('button', { name: /save changes/i }).click()
  await expect(page.getByText('Saved.')).toBeVisible()
  await expect(page.getByText('$123.45')).toBeVisible()
  await expect(page.getByText(/price set on/i)).toBeVisible()
})

test('proxy to an exact PriceCharting variant', async ({ page, freshUser }) => {
  const fresh = await freshUser()
  const entry = await createEntry(fresh.api, { display_name: `Variant Target ${stamp}` })

  await loginAs(page, fresh.name)
  await page.goto(entry.url)

  // Proxy then override with custom leaves customValue="123.45" and a
  // remembered proxy target, so switching back reopens "Change price
  // source", not a first-time picker.
  await page.getByRole('radio', { name: /proxy/i }).click()
  const picker = page.getByRole('dialog', { name: /choose a price source/i })
  await expect(picker).toBeVisible()
  await picker.getByRole('searchbox', { name: /search for games, hardware, and pricecharting/i }).fill('chrono trigger')
  await picker.getByRole('button', { name: 'Search', exact: true }).click()
  await picker.getByRole('button', { name: /Chrono Trigger on Super Nintendo Entertainment System/ }).first().click()
  await expect(page.getByText('Price source:')).toBeVisible()
  await page.getByRole('button', { name: 'Save changes' }).click()
  await expect(page.getByText('Saved.')).toBeVisible()
  await page.getByRole('radio', { name: /custom/i }).click()
  await page.getByLabel(/custom price \(usd\)/i).fill('123.45')
  await page.getByRole('button', { name: /save changes/i }).click()
  await expect(page.getByText('Saved.')).toBeVisible()

  // Proxy to an exact PC variant: search all of PC, pick Player's
  // Choice, copy prices as it.
  await page.getByRole('radio', { name: /proxy/i }).click()
  await page.getByRole('button', { name: /change price source/i }).click()
  await page.getByRole('radio', { name: 'PriceCharting' }).click()
  await page.getByLabel(/search for games, hardware, and pricecharting/i).fill('super mario 64')
  await page.getByRole('button', { name: 'Search', exact: true }).click()
  // first(): all-of-PC catalog carries regional prints, so both base
  // and variant titles recur.
  await expect(page.getByRole('button', { name: /use super mario 64$/i }).first()).toBeVisible()
  await page.getByRole('button', { name: /use super mario 64 \[player's choice\]/i }).first().click()
  await expect(page.getByText(/priced as "super mario 64 \[player's choice\]"/i)).toBeVisible()
  await page.getByRole('button', { name: /save changes/i }).click()
  await expect(page.getByText('Saved.')).toBeVisible()
  // Scoped to the Loose/CIB/New row, not a page-wide /\$\d/: the earlier
  // custom price leaves "Last custom price: $123.45" rendered too, which
  // would false-pass even if this proxy save failed to reprice.
  await expect(page.getByRole('region', { name: 'Pricing' }).getByText(/loose.*\$\d/i)).toBeVisible()
})

test('the library summary sums custom and proxy prices exactly', async ({ page, freshUser }) => {
  const fresh = await freshUser()
  await loginAs(page, fresh.name)
  await page.getByRole('link', { name: 'Collection', exact: true }).click()

  const valueCard = page
    .getByRole('region', { name: 'Totals' })
    .locator('div')
    .filter({ hasText: 'Collection value' })
  const totalValue = valueCard.getByText(/^\$[\d,]+\.\d{2}$/)
  const pricedNow = async (): Promise<number> => {
    const line = (await valueCard.getByText(/unpriced/).textContent()) ?? ''
    return Number(line.match(/(\d+) priced/)?.[1] ?? 'NaN')
  }
  const pricing = page.getByRole('region', { name: 'Pricing' })

  // Fresh identity starts at genuine zero; assertions check exact counts, not deltas.
  await expect(totalValue).toHaveText('$0.00')
  await expect(async () => {
    expect(await pricedNow()).toBe(0)
  }).toPass()

  // A custom-priced entry: the user's own number becomes its value.
  const custom = await createEntry(fresh.api, { display_name: `Summed Custom ${stamp}` })
  await page.goto(custom.url)
  await page.getByRole('radio', { name: /custom/i }).click()
  await page.getByLabel(/custom price \(usd\)/i).fill('42.50')
  await page.getByRole('button', { name: /save changes/i }).click()
  await expect(page.getByText('Saved.')).toBeVisible()
  const customLine = pricing.locator('p').first()
  await expect(customLine).toHaveText(/^\$[\d,]+\.\d{2}$/)
  const customShown = toCents((await customLine.textContent()) ?? '')
  expect(customShown).toBe(4250)

  // A proxy-priced entry: a real listing prices this copy.
  const proxy = await createEntry(fresh.api, { display_name: `Summed Proxy ${stamp}` })
  await page.goto(proxy.url)
  await page.getByRole('radio', { name: /proxy/i }).click()
  const picker = page.getByRole('dialog', { name: /choose a price source/i })
  await expect(picker).toBeVisible()
  await picker.getByRole('searchbox', { name: /search for games, hardware, and pricecharting/i }).fill('chrono trigger')
  await picker.getByRole('button', { name: 'Search', exact: true }).click()
  await picker.getByRole('button', { name: /Chrono Trigger on Super Nintendo Entertainment System/ }).first().click()
  await expect(page.getByText('Price source:')).toBeVisible()
  await page.getByRole('button', { name: /save changes/i }).click()
  await expect(page.getByText('Saved.')).toBeVisible()
  const proxyLine = pricing.locator('p').first()
  await expect(proxyLine).toHaveText(/^\$[\d,]+\.\d{2}$/)
  const proxyShown = toCents((await proxyLine.textContent()) ?? '')
  expect(proxyShown).toBeGreaterThan(0)

  // Once both priced, total equals exactly the sum of the two shown values.
  await page.getByRole('link', { name: 'Collection', exact: true }).click()
  await expect(page).toHaveURL(/\/collection$/)
  await expect(async () => {
    expect(await pricedNow()).toBe(2)
  }).toPass()
  expect(toCents((await totalValue.textContent()) ?? '')).toBe(customShown + proxyShown)
})

test('EUR display converts market values and pins the typed price', async ({ page, freshUser }) => {
  const fresh = await freshUser()
  await loginAs(page, fresh.name)
  await page.getByRole('link', { name: 'Collection', exact: true }).click()

  const selector = page.getByLabel('Display currency')
  await expect(selector).toBeEnabled()
  const pricing = page.getByRole('region', { name: 'Pricing' })

  // Optimistic switch re-renders without reload; toBeEnabled waits for
  // the currency PUT to settle.
  await selector.selectOption('EUR')
  await expect(selector).toHaveValue('EUR')
  await expect(selector).toBeEnabled()

  // The dashboard total re-labels in euros.
  await expect(page.getByText('Collection value (EUR)')).toBeVisible()

  // Seed helper sets currency directly (same field the add wizard
  // would send); wizard isn't under test here.
  const name = `EUR Pin ${stamp}`
  await createEntry(fresh.api, { display_name: name, currency: 'EUR' })

  // Currency freezes once at the pricing form's mount, from whatever
  // display currency has already resolved. A direct goto would mount
  // before EUR resolves and freeze USD; reload then navigate in instead.
  await page.reload()
  await expect(page.getByText('Collection value (EUR)')).toBeVisible()
  await page.getByRole('link', { name, exact: true }).click()
  await expect(page.getByText(/price paid \(eur\)/i)).toBeVisible()

  // Pin rule renders the entered EUR pair exactly as typed while EUR
  // is the display currency.
  await page.getByRole('radio', { name: /custom/i }).click()
  await page.getByLabel(/custom price \(eur\)/i).fill('60')
  await page.getByRole('button', { name: /save changes/i }).click()
  await expect(page.getByText('Saved.')).toBeVisible()
  await expect(pricing.getByText(/\u20ac60\.00/)).toBeVisible()
  await expect(pricing.getByText(/converted from usd at ecb rates/i)).toBeVisible()

  // USD snapshot = round(6000/rate) cents at whatever rate the stack
  // serves; read via /api/fx to avoid pinning a provider mode.
  const fx = await fresh.api.get('/api/fx')
  expect(fx.ok()).toBeTruthy()
  const rate = (await fx.json()).rates.EUR as number
  const usd = `$${(Math.round(6000 / rate) / 100).toFixed(2)}`

  // Back to USD: pin no longer applies (entered currency != display),
  // so the USD snapshot shows.
  await selector.selectOption('USD')
  await expect(pricing.getByText(usd)).toBeVisible()
  await expect(pricing.getByText('Market values are in USD.')).toBeVisible()

  // Back to EUR: the typed number re-pins exactly.
  await selector.selectOption('EUR')
  await expect(pricing.getByText(/\u20ac60\.00/)).toBeVisible()

  // Market quote is never pinned: point at a real listing, read USD,
  // then assert exact conversion at the snapshot rate.
  await page.getByRole('radio', { name: /proxy/i }).click()
  const picker = page.getByRole('dialog', { name: /choose a price source/i })
  await expect(picker).toBeVisible()
  await picker.getByRole('searchbox', { name: /search for games, hardware, and pricecharting/i }).fill('chrono trigger')
  await picker.getByRole('button', { name: 'Search', exact: true }).click()
  await picker.getByRole('button', { name: /Chrono Trigger on Super Nintendo Entertainment System/ }).first().click()
  await expect(page.getByText('Price source:')).toBeVisible()
  await page.getByRole('button', { name: /save changes/i }).click()
  await expect(page.getByText('Saved.')).toBeVisible()

  // Scoped to the first line so the leftover "Last custom price" note
  // can't stand in; USD prints the snapshot verbatim.
  const marketLine = pricing.locator('p').first()
  await selector.selectOption('USD')
  await expect(pricing.getByText('Market values are in USD.')).toBeVisible()
  await expect(marketLine).toHaveText(/^\$[\d,]+\.\d{2}$/)
  const marketUsdCents = toCents((await marketLine.textContent()) ?? '')
  expect(marketUsdCents).toBeGreaterThan(0)

  // Flip to EUR: same figure must convert at `rate`, formatted per the pinned locale.
  const marketEur = new Intl.NumberFormat('en-US', { style: 'currency', currency: 'EUR' }).format(
    (marketUsdCents / 100) * rate,
  )
  await selector.selectOption('EUR')
  await expect(pricing.getByText(/converted from usd at ecb rates/i)).toBeVisible()
  await expect(marketLine).toHaveText(marketEur)
})
