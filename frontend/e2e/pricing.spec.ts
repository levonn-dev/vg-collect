import { expect, loginAs, test } from './fixtures'
import { createEntry } from './seed'

// Five independent tests covering entry pricing: proxying to a picked
// listing, a custom price overriding an active proxy, proxying to an
// exact PriceCharting variant (and the leftover-form-state pitfall
// that flow can hide), the library summary's exact sum of a custom and
// a proxy price, and display-currency conversion. Every test mints its
// own freshUser: display currency and collection totals are both
// user-global state, so even the tests that never touch currency still
// need their own identity, or another test's priced entries would
// spoil the summary test's zero baseline. A fresh identity starts with
// nothing priced and nothing to clean up - teardown deletes the user
// (and every entry it owns) automatically, so no test here restores
// USD or sweeps its own entries at the end.
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

  // Selecting proxy flips the radio at once and opens the source
  // picker; nothing reaches the server until the form is saved.
  await page.getByRole('radio', { name: /proxy/i }).click()
  await expect(page.getByRole('radio', { name: /proxy/i })).toBeChecked()
  const picker = page.getByRole('dialog', { name: /choose a price source/i })
  await expect(picker).toBeVisible()
  const pickerSearch = picker.getByRole('searchbox', { name: /search for games, hardware, and pricecharting/i })
  // The picker prefills from the entry's own title (a custom entry with
  // no edition set, so the query is the title alone) and auto-fires the
  // search.
  await expect(pickerSearch).toHaveValue(name)
  await pickerSearch.fill('chrono trigger')
  await picker.getByRole('button', { name: 'Search', exact: true }).click()
  await picker.getByRole('button', { name: /Chrono Trigger on Super Nintendo Entertainment System/ }).first().click()
  await expect(page.getByText('Price source:')).toBeVisible()
  await page.getByRole('button', { name: 'Save changes' }).click()
  await expect(page.getByText('Saved.')).toBeVisible()
  // The proxied listing prices the copy: a dollar value appears (the
  // copy's value and the match breakdown both show one, so take the
  // first rather than assert a single match).
  await expect(page.getByRole('region', { name: 'Pricing' }).getByText(/\$\d/).first()).toBeVisible()
})

test('custom price overrides the market source', async ({ page, freshUser }) => {
  const fresh = await freshUser()
  const entry = await createEntry(fresh.api, { display_name: `Custom Override ${stamp}` })

  await loginAs(page, fresh.name)
  await page.goto(entry.url)

  // Proxy the entry to a market listing first: the override below needs
  // an active market source to override.
  await page.getByRole('radio', { name: /proxy/i }).click()
  const picker = page.getByRole('dialog', { name: /choose a price source/i })
  await expect(picker).toBeVisible()
  await picker.getByRole('searchbox', { name: /search for games, hardware, and pricecharting/i }).fill('chrono trigger')
  await picker.getByRole('button', { name: 'Search', exact: true }).click()
  await picker.getByRole('button', { name: /Chrono Trigger on Super Nintendo Entertainment System/ }).first().click()
  await expect(page.getByText('Price source:')).toBeVisible()
  await page.getByRole('button', { name: 'Save changes' }).click()
  await expect(page.getByText('Saved.')).toBeVisible()

  // --- Custom price on the proxied entry: the user's own number
  // overrides the market source, packaging-independent.
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

  // Arrange: proxy the entry to a market listing, then override with a
  // custom price - this leaves the form's customValue state at "123.45"
  // and a remembered proxy target, so switching back to proxy below
  // reopens "Change price source" instead of a first-time picker.
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

  // --- Proxy to an exact PriceCharting variant listing: search all
  // of PC, pick the Player's Choice row, and the copy prices as it.
  await page.getByRole('radio', { name: /proxy/i }).click()
  await page.getByRole('button', { name: /change price source/i }).click()
  await page.getByRole('radio', { name: 'PriceCharting' }).click()
  await page.getByLabel(/search for games, hardware, and pricecharting/i).fill('super mario 64')
  await page.getByRole('button', { name: 'Search', exact: true }).click()
  // Both the base and the variant row surface, each with a price line.
  // first(): the all-of-PC catalog carries regional prints, so the exact
  // base title and the Player's Choice variant each recur across regions.
  await expect(page.getByRole('button', { name: /use super mario 64$/i }).first()).toBeVisible()
  await page.getByRole('button', { name: /use super mario 64 \[player's choice\]/i }).first().click()
  await expect(page.getByText(/priced as "super mario 64 \[player's choice\]"/i)).toBeVisible()
  await page.getByRole('button', { name: /save changes/i }).click()
  await expect(page.getByText('Saved.')).toBeVisible()
  // The variant listing prices the copy: assert the resolved proxy's own
  // price line (the "Priced as" card's Loose/CIB/New row), not just any
  // dollar sign on the page. The custom-price segment above leaves the
  // form's customValue at "123.45", so PricingPanel still renders "Last
  // custom price: $123.45" while in proxy mode - a page-wide /\$\d/ would
  // pass on that leftover even if this proxy save failed to reprice the
  // entry. Scoping to the Loose/CIB/New row (stub prices are id-seeded,
  // real ones vary by day, so assert a value is shown, not a number) can
  // only pass once the resolved pc_listing's own pricing is displayed.
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

  // A fresh identity starts with nothing priced: the total is a
  // genuine zero, so the assertions below check exact counts and sums
  // instead of a before/after delta.
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

  // Back on the dashboard: once both have priced, the total equals
  // exactly the sum of the two shown values.
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

  // Switch the profile to EUR from the header. The choice is optimistic,
  // so every price re-renders at once without a reload; toBeEnabled
  // waits for the currency PUT to actually settle (the select disables
  // while saving), which the reload below depends on.
  await selector.selectOption('EUR')
  await expect(selector).toHaveValue('EUR')
  await expect(selector).toBeEnabled()

  // The dashboard total re-labels in euros.
  await expect(page.getByText('Collection value (EUR)')).toBeVisible()

  // A custom (off-catalog) entry, stamped with EUR as its price-paid
  // currency: the seed helper sets it directly (the same field the add
  // wizard would send from the profile currency at create time), since
  // the wizard is not what this flow is testing.
  const name = `EUR Pin ${stamp}`
  await createEntry(fresh.api, { display_name: name, currency: 'EUR' })

  // Open the entry through the collection list rather than a direct
  // goto. The custom-price input's currency freezes once, at the
  // pricing form's own mount, from whatever the display currency has
  // already resolved to (so a rate snapshot arriving mid-edit can never
  // silently reinterpret already-typed text) - a goto straight to the
  // entry would mount that form on a brand new page load, before the
  // page has had any chance to resolve the EUR preference and rate
  // snapshot, freezing USD instead. Reloading the already-EUR
  // collection page first, then navigating in through its own link,
  // lets both resolve before the pricing form ever mounts.
  await page.reload()
  await expect(page.getByText('Collection value (EUR)')).toBeVisible()
  await page.getByRole('link', { name, exact: true }).click()
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
  // the stack is serving (stub or real) - read via the same fresh
  // user's session, so the test stays exact without pinning a provider
  // mode.
  const fx = await fresh.api.get('/api/fx')
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
  await picker.getByRole('searchbox', { name: /search for games, hardware, and pricecharting/i }).fill('chrono trigger')
  await picker.getByRole('button', { name: 'Search', exact: true }).click()
  await picker.getByRole('button', { name: /Chrono Trigger on Super Nintendo Entertainment System/ }).first().click()
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
})
