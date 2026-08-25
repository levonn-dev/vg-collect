import { entryIdFromURL, expect, test } from './fixtures'
import { deleteEntry } from './seed'

// Runs against the real IGDB catalog (not stubbed). igdb_game_id 11227 /
// platform_igdb_id 19 (SNES) is the same pair bruno/enrichment/resolve-game.bru targets.
const stamp = `e2e-region-${Date.now()}`

// Switcher's accessible name is in the active locale; match whichever is live.
const localeComboName = /^(Language|言語)$/

test('region-localized add: matched_region search card, wizard region default, locale-switched entry title', async ({ page, api }) => {
  test.setTimeout(60_000)
  await page.goto('/')

  // Set only after add succeeds; finally only deletes when non-empty.
  let entryUrl = ''
  try {
    // Real catalog matches both the 1995 SNES original (11227) and a 2020
    // remake (119391); anchor on the SNES platform chip, not list position,
    // since IGDB's real search order is not stable.
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
    // Region lives on platform chips, not the card; SNES chip badges
    // Korea (its only IGDB release row).
    await expect(snesRow.getByRole('button', { name: /on Super Famicom/ })).toHaveAccessibleName(/\(NTSC-J\)/)
    await expect(snesRow.getByRole('button', { name: snesChipName })).toHaveAccessibleName(/\(Korea\)/)
    await snesRow.getByRole('button', { name: snesChipName }).click()

    // Chip's region set [korea] outranks matched_region ja-JP, so the
    // wizard defaults korea (canonical heading); picking NTSC-J switches
    // to the romaji ja-JP bundle.
    await expect(page.getByLabel('Region')).toHaveValue('korea')
    await expect(page.getByRole('heading', { name: 'Your copy of Trials of Mana' })).toBeVisible()
    await page.getByLabel('Region').selectOption('ntsc_j')
    await expect(page.getByRole('heading', { name: 'Your copy of Seiken Densetsu 3' })).toBeVisible()
    await page.getByLabel('Notes').fill(stamp)
    await page.getByRole('button', { name: 'Continue' }).click()

    // Live PriceCharting match surfaces on confirm; only proving it
    // appears, not pricing depth.
    await expect(page.getByText('Priced as "Seiken Densetsu 3" (Super Famicom)', { exact: false })).toBeVisible()
    await page.getByRole('button', { name: 'Add to collection' }).click()
    await expect(page).toHaveURL(/\/entries\//)
    entryUrl = page.url()

    // pickLocalization resolves region ntsc_j to the ja-JP bundle at create time.
    await expect(page.getByRole('heading', { name: 'Seiken Densetsu 3' })).toBeVisible()
    await expect(page.getByText('Trials of Mana', { exact: true })).toBeVisible()

    // Square is the credited developer on the live catalog row (loose
    // match tolerates co-credits).
    await expect(page.getByText(/Developed by .*Square/)).toBeVisible()

    // Switching locale renders the entry's native-script title.
    await page.getByRole('combobox', { name: 'Language' }).selectOption('ja')
    await expect(page.getByRole('heading', { name: '聖剣伝説 3' })).toBeVisible()

    // Switcher found under its Japanese label; title flips back (same
    // round trip locale.spec.ts proves for nav chrome).
    await page.getByRole('combobox', { name: '言語' }).selectOption('en')
    await expect(page.getByRole('heading', { name: 'Seiken Densetsu 3' })).toBeVisible()
  } finally {
    // Locale restore first: delete button is looked up by its English
    // label. Each step independently guarded.
    try {
      await page.getByRole('combobox', { name: localeComboName }).selectOption('en')
    } catch (err) {
      console.log('teardown: locale restore failed:', err)
    }
    if (entryUrl) {
      try {
        await deleteEntry(api, entryIdFromURL(entryUrl))
      } catch (err) {
        console.log(`teardown: entry delete at ${entryUrl} failed:`, err)
      }
    }
  }
})

test('native-script search: a pure Japanese-script query finds the SNES release through the real IGDB localization leg', async ({ page }) => {
  test.setTimeout(30_000)
  await page.goto('/')

  // Pure native-script query: only the SearchLocalizations leg can
  // surface it. Read-only, no cleanup.
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
  await page.goto('/')

  // Puyo Puyo SUN (igdb_game_id 3578) matches on canonical name, so no
  // matched_region; Sega Saturn chip's release_regions ["japan"] badges
  // NTSC-J. Anchor on that chip: the PS-family release is a separate card.
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

  // Chip's region set [ntsc_j] supplies the wizard default directly;
  // no matched_region involved.
  await expect(page.getByLabel('Region')).toHaveValue('ntsc_j')

  // Picked chip leads the select: single-region platform groups as its
  // own group plus Other regions.
  const saturnGroup = page.locator('optgroup[label="Released on Sega Saturn"]')
  await expect(saturnGroup.locator('option')).toHaveText(['NTSC-J'])
  await expect(page.locator('optgroup[label="Other regions"] option')).toHaveText(['Choose...', 'NTSC-U', 'PAL', 'Korea', 'Brazil', 'China', 'Region free'])
})

test('hardware add defaults the region from the listing console axis', async ({ page }) => {
  test.setTimeout(30_000)
  await page.goto('/')

  // Hardware picks carry no matched_region or platform chip;
  // consoleRegionFor seeds ntsc_j off the JP-market console name.
  // first(): search also returns regional/bundled variants.
  await page.getByRole('link', { name: 'Add', exact: true }).click()
  await page.getByRole('radio', { name: /hardware/i }).check()
  await page.getByRole('searchbox', { name: /search for games and hardware/i }).fill('super famicom console')
  await page.getByRole('button', { name: 'Search', exact: true }).click()

  const addName = /Add Super Famicom Console/
  const row = page.getByRole('listitem').filter({ has: page.getByRole('button', { name: addName }) }).first()
  await expect(row).toBeVisible()
  await row.getByRole('button', { name: addName }).click()

  // suggestedRegion lands ntsc_j directly off the console axis; nothing
  // created, no teardown.
  await expect(page.getByLabel('Region')).toHaveValue('ntsc_j')
})
