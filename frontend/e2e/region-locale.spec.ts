import { expect, test, type Page } from '@playwright/test'

// Region-localized add journey against the real IGDB catalog: search
// "Seiken Densetsu 3" (the romaji query), pick the SNES platform chip
// on the matched_region row, confirm the platform-first wizard default
// (a bare korea-only chip falls back to the matched region) and the
// region-following heading, complete the add, then round-trip the
// entry detail page's title through the footer's ja locale switch and
// back. igdb_game_id 11227 on platform_igdb_id 19 (Super Nintendo
// Entertainment System) is the same id/platform pair
// bruno/enrichment/resolve-game.bru now targets.
//
// A second test below covers the gap that leaves: a pure native-script
// query is Latin-free, so it trips hasNonLatinLetter and the search
// runs through enrichment's SearchLocalizations where-filter leg
// against game_localizations - not covered by IGDB's primary search
// index, and until this test the leg's real-provider behavior was
// exercised only through its stub. It only searches, so it carries no
// teardown.
//
// A third test below covers a different case: a canonical-name search
// carries no matched_region at all, so the platform chips are the
// only region picker in play. Puyo Puyo SUN's Sega Saturn platform
// chip carries release_regions ["japan"], listed in the chip's own
// accessible name as NTSC-J, and - platform-first - seeds the
// wizard's region default the same way, no matched_region involved.
// It stops at the details step - no submit, so no created entry and
// no teardown either.
const stamp = `e2e-region-${Date.now()}`

async function login(page: Page) {
  await page.goto('/api/auth/login?provider=dev&user=bob')
  await expect(page.getByRole('navigation', { name: 'Primary' })).toBeVisible()
}

// Accept the next native dialog (the delete-entry confirm) once.
function acceptNext(page: Page) {
  page.once('dialog', (d) => void d.accept())
}

// The footer switcher's own accessible name is rendered in the active
// locale - "Language" in English, its own Japanese name once switched
// (the literal below) - so match whichever one is live right now
// rather than assuming which locale the page is still in.
const localeComboName = /^(Language|言語)$/

test('region-localized add: matched_region search card, wizard region default, locale-switched entry title', async ({ page }) => {
  test.setTimeout(60_000)
  await login(page)

  // Set once the add succeeds, below; the finally block below only
  // attempts a delete when this is non-empty, so a failure before the
  // entry exists never tries to clean up something that was never
  // created.
  let entryUrl = ''
  try {
    // --- Search: on the real catalog, both the 1995 SNES original
    // (igdb_game_id 11227) and a 2020 remake (119391) match, and both
    // carry matched_region ja-JP - only the original lists Super
    // Nintendo Entertainment System among its platforms, so anchoring
    // the row on that platform chip (rather than list position) finds
    // the SNES release deterministically even if IGDB's search order
    // shifts between runs.
    await page.getByRole('link', { name: 'Add', exact: true }).click()
    await page.getByRole('searchbox', { name: /search for games and hardware/i }).fill('Seiken Densetsu 3')
    await page.getByRole('button', { name: 'Search', exact: true }).click()

    const snesChipName = /on Super Nintendo Entertainment System/
    const snesRow = page
      .getByRole('listitem')
      .filter({ has: page.getByRole('button', { name: snesChipName }) })
      .first()
    await expect(snesRow.getByText('Seiken Densetsu 3', { exact: true })).toBeVisible()
    await expect(snesRow.getByText('Trials of Mana', { exact: true })).toBeVisible()
    // The region signal lives on the platform chips now (the card
    // itself carries none): the Super Famicom chip lists its JP-only
    // release, while the SNES chip anchoring this row is bare - its
    // only IGDB release row is korea, which maps to no entry region
    // yet.
    await expect(snesRow.getByRole('button', { name: /on Super Famicom/ })).toHaveAccessibleName(/\(NTSC-J\)/)
    await snesRow.getByRole('button', { name: snesChipName }).click()

    // --- Details: the SNES chip carries no mappable regions, so the
    // platform-first default falls back to the matched-region mapping
    // (ja-JP -> ntsc_j), and the heading follows the selected region:
    // ntsc_j resolves the ja-JP bundle, rendered in romaji under the
    // en locale.
    await expect(page.getByRole('heading', { name: 'Your copy of Seiken Densetsu 3' })).toBeVisible()
    await expect(page.getByLabel('Region')).toHaveValue('ntsc_j')
    await page.getByLabel('Notes').fill(stamp)
    await page.getByRole('button', { name: 'Continue' }).click()

    // --- Confirm and add. This product carries no PriceCharting match
    // on this stack yet (a first-time resolve against the live
    // catalog), so the confirm card is the unmatched variant; the
    // wizard still completes the add either way; this journey is about
    // localization, not pricing.
    await page.getByRole('button', { name: 'Add to collection' }).click()
    await expect(page).toHaveURL(/\/entries\//)
    entryUrl = page.url()

    // --- Entry detail, en locale: the romanized title plus the
    // canonical secondary line (the collection service's pickLocalization
    // resolves region ntsc_j to the product's ja-JP bundle at create time).
    await expect(page.getByRole('heading', { name: 'Seiken Densetsu 3' })).toBeVisible()
    await expect(page.getByText('Trials of Mana', { exact: true })).toBeVisible()

    // --- Switch to Japanese via the footer switcher: the same entry
    // renders its native-script title.
    await page.getByRole('combobox', { name: 'Language' }).selectOption('ja')
    await expect(page.getByRole('heading', { name: '聖剣伝説 3' })).toBeVisible()

    // --- Switch back to English (the switcher's own label is rendered
    // in the active locale, so it is found under its Japanese name) and
    // confirm the title flips back too - the same round trip
    // locale.spec.ts proves for the nav chrome, here for a localized
    // product title.
    await page.getByRole('combobox', { name: '言語' }).selectOption('en')
    await expect(page.getByRole('heading', { name: 'Seiken Densetsu 3' })).toBeVisible()
  } finally {
    // Runs whether the try block completed or threw, so a failed
    // assertion never leaves a stamped orphaned entry or the browser
    // stuck on ja. Locale restore goes first: the delete button below
    // is looked up by its English label, so a failure between the two
    // switches would otherwise strand the page in Japanese and break
    // the delete step too. The two steps are independently guarded so
    // one failing step never blocks or masks the other.
    try {
      await page.getByRole('combobox', { name: localeComboName }).selectOption('en')
    } catch (err) {
      console.log('teardown: locale restore failed:', err)
    }
    if (entryUrl) {
      try {
        await page.goto(entryUrl)
        acceptNext(page)
        await page.getByRole('button', { name: 'Delete entry' }).click()
        await expect(page).toHaveURL(/\/collection$/)
      } catch (err) {
        console.log(`teardown: entry delete at ${entryUrl} failed:`, err)
      }
    }
  }
})

test('native-script search: a pure Japanese-script query finds the SNES release through the real IGDB localization leg', async ({ page }) => {
  test.setTimeout(30_000)
  await login(page)

  // Unlike the romaji query above (pure Latin), this is the raw
  // native-script title: only the SearchLocalizations leg can surface
  // it (see the file header for why). Read-only: no cleanup needed.
  await page.getByRole('link', { name: 'Add', exact: true }).click()
  await page.getByRole('searchbox', { name: /search for games and hardware/i }).fill('聖剣伝説 3')
  await page.getByRole('button', { name: 'Search', exact: true }).click()

  const snesChipName = /on Super Nintendo Entertainment System/
  const snesRow = page
    .getByRole('listitem')
    .filter({ has: page.getByRole('button', { name: snesChipName }) })
    .first()
  await expect(snesRow.getByText('Trials of Mana', { exact: true })).toBeVisible()
})

test('region picker chips: canonical-name search lists Puyo Puyo SUN Sega Saturn NTSC-J, seeds the platform-first default', async ({ page }) => {
  test.setTimeout(30_000)
  await login(page)

  // Puyo Puyo SUN (igdb_game_id 3578, the 1997 Sega Saturn/Nintendo 64/PC
  // original) matches this query on its canonical name, so the result
  // carries no matched_region - the only region signal is each
  // platform chip's own release_regions list, read straight off its
  // accessible name. The Sega Saturn chip's release_regions is
  // ["japan"], listed as NTSC-J. The Mega Drive "Vs. Puyo Puyo Sun"
  // game is a worldwide release, so its chip now lists all three
  // regions (it used to show none). The PlayStation-family "Puyo Puyo
  // SUN Ketteiban" release is a separate search-result card, so
  // anchoring on the Sega Saturn chip finds this one deterministically.
  await page.getByRole('link', { name: 'Add', exact: true }).click()
  await page.getByRole('searchbox', { name: /search for games and hardware/i }).fill('Puyo Puyo Sun')
  await page.getByRole('button', { name: 'Search', exact: true }).click()

  const saturnChipName = /on Sega Saturn/
  const saturnRow = page
    .getByRole('listitem')
    .filter({ has: page.getByRole('button', { name: saturnChipName }) })
    .first()
  const saturnChip = saturnRow.getByRole('button', { name: saturnChipName })
  await expect(saturnChip).toHaveAccessibleName(/\(NTSC-J\)/)
  await saturnChip.click()

  // --- Details: platform-first, the chip's own region set [ntsc_j]
  // supplies the wizard's default directly - no matched_region is
  // involved, canonical-name search or not.
  await expect(page.getByLabel('Region')).toHaveValue('ntsc_j')

  // The picked chip's set leads the select: a one-region platform
  // groups as [Released on Sega Saturn: NTSC-J][Other regions: rest].
  const saturnGroup = page.locator('optgroup[label="Released on Sega Saturn"]')
  await expect(saturnGroup.locator('option')).toHaveText(['NTSC-J'])
  await expect(page.locator('optgroup[label="Other regions"] option')).toHaveText(['NTSC-U', 'PAL', 'REGION-FREE'])
})
