import { i18n } from '@lingui/core'
import { cleanup, screen, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { messages as jaMessages } from '../../locales/ja.po'
import { jsonResponse } from '../../test/fixtures'
import { renderWithMoney } from '../../test/money'
import type { GamePick } from './SearchPicker'
import SearchPicker from './SearchPicker'

function renderPicker(
  props: Partial<Parameters<typeof SearchPicker>[0]> = {},
  opts: { currency?: string } = {},
) {
  const onPick = vi.fn()
  renderWithMoney(<SearchPicker onPick={onPick} {...props} />, opts)
  return onPick
}

afterEach(() => {
  // Order matters: cleanup() before activate() - see EntryTable.test.tsx's
  // afterEach for why (I18nProvider update outside act otherwise).
  vi.unstubAllGlobals()
  cleanup()
  i18n.activate('en')
})

function activateJa() {
  i18n.load('ja', jaMessages)
  i18n.activate('ja')
}

const gameResults = {
  degraded: false,
  results: [{
    type: 'game', name: 'Chrono Trigger', igdb_game_id: 1000,
    first_release_date: '1995-03-11', cover_url: 'https://img.example/ct.jpg',
    platforms: [
      { igdb_platform_id: 6, name: 'SNES' },
      { igdb_platform_id: 8, name: 'PlayStation' },
    ],
  }],
}

it('searches games and picks a platform', async () => {
  const fetchMock = vi.fn().mockResolvedValue(jsonResponse(200, gameResults))
  vi.stubGlobal('fetch', fetchMock)
  const onPick = renderPicker()
  await userEvent.type(screen.getByRole('searchbox', { name: /search/i }), 'chrono')
  await userEvent.click(screen.getByRole('button', { name: 'Search' }))
  expect(await screen.findByText('Chrono Trigger')).toBeInTheDocument()
  expect(String(fetchMock.mock.calls[0][0])).toContain('type=game&q=chrono')
  await userEvent.click(screen.getByRole('button', { name: 'Chrono Trigger on SNES' }))
  expect(onPick).toHaveBeenCalledWith({
    kind: 'game', igdbGameId: 1000, name: 'Chrono Trigger', platformId: 6, platformName: 'SNES',
    coverUrl: gameResults.results[0].cover_url, firstReleaseDate: gameResults.results[0].first_release_date,
  })
})

// The JP trio: a region-localized result carrying both a native-script
// title and its transliteration plus its own box art, matching the
// fixture productTitle.test.ts exercises. r.name ('Trials of Mana')
// stands in for the canonical/community name a plain query would
// otherwise show.
const jpMatchedResult = {
  type: 'game', name: 'Trials of Mana', igdb_game_id: 2000,
  cover_url: 'https://img.example/na.jpg',
  platforms: [{ igdb_platform_id: 6, name: 'SNES' }],
  matched_region: 'ja-JP',
  localizations: [
    { region: 'ja-JP', name: '聖剣伝説 3', translit: 'Seiken Densetsu 3', cover_url: 'https://x/jp.jpg' },
  ],
}

it('renders the localized title, canonical secondary, and JP cover on a matched_region hit', async () => {
  vi.stubGlobal('fetch', vi.fn().mockResolvedValue(jsonResponse(200, { degraded: false, results: [jpMatchedResult] })))
  renderPicker({ initialQuery: 'trials' })
  const title = await screen.findByText('Seiken Densetsu 3')
  expect(title).toHaveAttribute('lang', 'ja-Latn')
  expect(screen.getByText('Trials of Mana')).toBeInTheDocument()
  expect(document.querySelector('img')).toHaveAttribute('src', 'https://x/jp.jpg')
  // The card itself carries no region chip anymore - region signals
  // live on the platform chips (none here: this SNES platform ref has
  // no release_regions).
  expect(screen.queryByText('NTSC-J')).not.toBeInTheDocument()
})

it('renders the native title under the ja locale for a matched_region hit', async () => {
  activateJa()
  vi.stubGlobal('fetch', vi.fn().mockResolvedValue(jsonResponse(200, { degraded: false, results: [jpMatchedResult] })))
  renderPicker({ initialQuery: 'trials' })
  const title = await screen.findByText('聖剣伝説 3')
  expect(title).toHaveAttribute('lang', 'ja')
})

// Same bundle data as jpMatchedResult, but the query matched the
// canonical name (no matched_region) - the row must render exactly as
// it would with no localizations at all.
const jpUnmatchedResult = {
  type: 'game', name: 'Trials of Mana', igdb_game_id: 2000,
  cover_url: 'https://img.example/na.jpg',
  platforms: [{ igdb_platform_id: 6, name: 'SNES' }],
  localizations: [
    { region: 'ja-JP', name: '聖剣伝説 3', translit: 'Seiken Densetsu 3', cover_url: 'https://x/jp.jpg' },
  ],
}

it('renders canonically when localizations are present but matched_region is absent', async () => {
  vi.stubGlobal('fetch', vi.fn().mockResolvedValue(jsonResponse(200, { degraded: false, results: [jpUnmatchedResult] })))
  renderPicker({ initialQuery: 'trials' })
  const title = await screen.findByText('Trials of Mana')
  expect(title).not.toHaveAttribute('lang')
  expect(screen.queryByText('Seiken Densetsu 3')).not.toBeInTheDocument()
  expect(screen.queryByText('NTSC-J')).not.toBeInTheDocument()
  expect(document.querySelector('img')).toHaveAttribute('src', 'https://img.example/na.jpg')
})

it('includes the matched region as a suggestion on a game platform pick', async () => {
  vi.stubGlobal('fetch', vi.fn().mockResolvedValue(jsonResponse(200, { degraded: false, results: [jpMatchedResult] })))
  const onPick = renderPicker({ initialQuery: 'trials' })
  await userEvent.click(await screen.findByRole('button', { name: 'Trials of Mana on SNES' }))
  expect(onPick).toHaveBeenCalledWith({
    kind: 'game', igdbGameId: 2000, name: 'Trials of Mana', platformId: 6, platformName: 'SNES',
    suggestedRegion: 'ntsc_j',
    localizations: jpMatchedResult.localizations,
    coverUrl: jpMatchedResult.cover_url,
  })
})

// ko-KR is a real, open-world region identifier (schema comment on
// SearchResult.matched_region) with no REGION_FROM_MATCH entry today:
// the title still swaps (that only needs a matching bundle), but no
// chip renders and no suggestion rides the pick.
const koMatchedResult = {
  type: 'game', name: 'Trials of Mana', igdb_game_id: 2001,
  cover_url: 'https://img.example/na.jpg',
  platforms: [{ igdb_platform_id: 6, name: 'SNES' }],
  matched_region: 'ko-KR',
  localizations: [
    { region: 'ko-KR', name: '트라이얼즈 오브 마나', translit: 'Teuraieoljeu obeu Mana', cover_url: 'https://x/kr.jpg' },
  ],
}

it('swaps the title for an unmapped matched_region but suggests no region', async () => {
  vi.stubGlobal('fetch', vi.fn().mockResolvedValue(jsonResponse(200, { degraded: false, results: [koMatchedResult] })))
  const onPick = renderPicker({ initialQuery: 'trials' })
  expect(await screen.findByText('Teuraieoljeu obeu Mana')).toBeInTheDocument()
  expect(screen.queryByText(/^(NTSC-U|NTSC-J|PAL|Region free)$/)).not.toBeInTheDocument()
  await userEvent.click(screen.getByRole('button', { name: 'Trials of Mana on SNES' }))
  expect(onPick).toHaveBeenCalledWith({
    kind: 'game', igdbGameId: 2001, name: 'Trials of Mana', platformId: 6, platformName: 'SNES',
    localizations: koMatchedResult.localizations,
    coverUrl: koMatchedResult.cover_url,
  })
})

// EU (a continent-form region identifier) carries no BCP-47 language
// subtag (bundleLang('EU') is undefined) - the swapped title must
// render with no lang attribute at all, never the string
// "undefined-Latn".
const euMatchedResult = {
  type: 'game', name: 'Trials of Mana', igdb_game_id: 2002,
  cover_url: 'https://img.example/na.jpg',
  platforms: [{ igdb_platform_id: 6, name: 'SNES' }],
  matched_region: 'EU',
  localizations: [
    { region: 'EU', translit: 'Trials of Mana (EU)', cover_url: 'https://x/eu.jpg' },
  ],
}

it('renders an EU match with no chip and never an "undefined-Latn" lang attribute', async () => {
  vi.stubGlobal('fetch', vi.fn().mockResolvedValue(jsonResponse(200, { degraded: false, results: [euMatchedResult] })))
  renderPicker({ initialQuery: 'trials' })
  const title = await screen.findByText('Trials of Mana (EU)')
  expect(title).not.toHaveAttribute('lang')
  expect(screen.queryByText('PAL')).not.toBeInTheDocument()
})

// The region picker: a platform chip's own release_regions (distinct
// from matched_region, which is about the searched name/title) become
// that chip's mapped region list, seeding the wizard's region step
// platform-first - the matched-region mapping only breaks ties within
// the chip's own set (see the precedence tests below). gameResults
// above (no release_regions on either platform) is the untouched
// regression fixture for the bare-chip case.
const saturnAvailabilityResult = {
  type: 'game', name: 'Panzer Dragoon Saga', igdb_game_id: 3000,
  platforms: [{ igdb_platform_id: 32, name: 'Sega Saturn', release_regions: ['japan'] }],
}

it('renders an availability badge on a platform chip, folded into the aria-label', async () => {
  vi.stubGlobal('fetch', vi.fn().mockResolvedValue(jsonResponse(200, { degraded: false, results: [saturnAvailabilityResult] })))
  renderPicker({ initialQuery: 'panzer' })
  const chip = await screen.findByRole('button', { name: 'Panzer Dragoon Saga on Sega Saturn (NTSC-J)' })
  expect(within(chip).getByText('NTSC-J')).toBeInTheDocument()
})

const multiRegionAvailabilityResult = {
  type: 'game', name: 'Actraiser 2', igdb_game_id: 3001,
  platforms: [{ igdb_platform_id: 6, name: 'SNES', release_regions: ['north_america', 'europe'] }],
}

it('joins multiple availability badges on a chip with a slash', async () => {
  vi.stubGlobal('fetch', vi.fn().mockResolvedValue(jsonResponse(200, { degraded: false, results: [multiRegionAvailabilityResult] })))
  renderPicker({ initialQuery: 'actraiser' })
  const chip = await screen.findByRole('button', { name: 'Actraiser 2 on SNES (NTSC-U/PAL)' })
  expect(within(chip).getByText('NTSC-U/PAL')).toBeInTheDocument()
})

const worldwideResult = {
  type: 'game', name: 'Tetris', igdb_game_id: 3002,
  platforms: [{ igdb_platform_id: 6, name: 'SNES', release_regions: ['worldwide'] }],
}

it('expands a worldwide release to the full region list on the chip', async () => {
  vi.stubGlobal('fetch', vi.fn().mockResolvedValue(jsonResponse(200, { degraded: false, results: [worldwideResult] })))
  renderPicker({ initialQuery: 'tetris' })
  const chip = await screen.findByRole('button', { name: 'Tetris on SNES (NTSC-U/NTSC-J/PAL)' })
  expect(within(chip).getByText('NTSC-U/NTSC-J/PAL')).toBeInTheDocument()
})

const allRegionsResult = {
  type: 'game', name: 'Tetris Attack', igdb_game_id: 3003,
  platforms: [{ igdb_platform_id: 6, name: 'SNES', release_regions: ['japan', 'north_america', 'europe'] }],
}

it('keeps the full list when release regions cover all three entry regions', async () => {
  vi.stubGlobal('fetch', vi.fn().mockResolvedValue(jsonResponse(200, { degraded: false, results: [allRegionsResult] })))
  renderPicker({ initialQuery: 'tetris attack' })
  const chip = await screen.findByRole('button', { name: 'Tetris Attack on SNES (NTSC-J/NTSC-U/PAL)' })
  expect(within(chip).getByText('NTSC-J/NTSC-U/PAL')).toBeInTheDocument()
})

const matchedOverAvailabilityResult = {
  type: 'game', name: 'Trials of Mana', igdb_game_id: 4000,
  cover_url: 'https://img.example/na.jpg',
  platforms: [{ igdb_platform_id: 6, name: 'SNES', release_regions: ['north_america'] }],
  matched_region: 'ja-JP',
  localizations: [
    { region: 'ja-JP', name: '聖剣伝説 3', translit: 'Seiken Densetsu 3', cover_url: 'https://x/jp.jpg' },
  ],
}

// Platform-first precedence: the clicked chip names the physical copy,
// so its own regions outrank the matched name - the matched mapping
// only wins when it is in the chip's set.
it('pick seeds the chip region over a conflicting matched region', async () => {
  vi.stubGlobal('fetch', vi.fn().mockResolvedValue(jsonResponse(200, { degraded: false, results: [matchedOverAvailabilityResult] })))
  const onPick = renderPicker({ initialQuery: 'trials' })
  await userEvent.click(await screen.findByRole('button', { name: 'Trials of Mana on SNES (NTSC-U)' }))
  expect(onPick).toHaveBeenCalledWith(expect.objectContaining({ suggestedRegion: 'ntsc_u', regions: ['ntsc_u'] }))
})

// The Wii-shape tiebreak: the matched region IS in the chip's set, so
// it wins over the earliest-release default. (This also proves the
// matched region outranks the en home region: ntsc_u is in the chip
// set too, yet ja-JP wins.)
const matchedWithinAvailabilityResult = {
  type: 'game', name: 'Secret of Mana', igdb_game_id: 4002,
  platforms: [{ igdb_platform_id: 41, name: 'Wii', release_regions: ['japan', 'north_america', 'australia', 'europe'] }],
  matched_region: 'ja-JP',
  localizations: [{ region: 'ja-JP', translit: 'Seiken Densetsu 2' }],
}

it('pick seeds the matched region when the chip set contains it', async () => {
  vi.stubGlobal('fetch', vi.fn().mockResolvedValue(jsonResponse(200, { degraded: false, results: [matchedWithinAvailabilityResult] })))
  const onPick = renderPicker({ initialQuery: 'seiken' })
  await userEvent.click(await screen.findByRole('button', { name: 'Secret of Mana on Wii (NTSC-J/NTSC-U/PAL)' }))
  expect(onPick).toHaveBeenCalledWith(expect.objectContaining({
    suggestedRegion: 'ntsc_j', regions: ['ntsc_j', 'ntsc_u', 'pal'],
  }))
})

// The home-region arm: a canonical-name search (no matched region) on
// a chip released everywhere must not default to the earliest release
// region (JP-first worldwide games would all land NTSC-J) - the UI
// locale's home region wins when the chip's set contains it.
const jpFirstWorldwideResult = {
  type: 'game', name: 'Super Mario 64', igdb_game_id: 4004,
  platforms: [{ igdb_platform_id: 4, name: 'Nintendo 64', release_regions: ['japan', 'north_america', 'europe'] }],
}

it('pick seeds the locale home region on a no-match multi-region chip', async () => {
  vi.stubGlobal('fetch', vi.fn().mockResolvedValue(jsonResponse(200, { degraded: false, results: [jpFirstWorldwideResult] })))
  const onPick = renderPicker({ initialQuery: 'mario' })
  await userEvent.click(await screen.findByRole('button', { name: 'Super Mario 64 on Nintendo 64 (NTSC-J/NTSC-U/PAL)' }))
  expect(onPick).toHaveBeenCalledWith(expect.objectContaining({ suggestedRegion: 'ntsc_u' }))
})

it('pick seeds the ja home region for the same chip under the ja locale', async () => {
  activateJa()
  vi.stubGlobal('fetch', vi.fn().mockResolvedValue(jsonResponse(200, { degraded: false, results: [jpFirstWorldwideResult] })))
  const onPick = renderPicker({ initialQuery: 'mario' })
  await userEvent.click(await screen.findByRole('button', { name: /Super Mario 64/ }))
  expect(onPick).toHaveBeenCalledWith(expect.objectContaining({ suggestedRegion: 'ntsc_j' }))
})

// A chip whose rows are all unmapped (korea today) stays bare and
// carries no regions; the matched mapping still seeds the default.
const unmappedOnlyResult = {
  type: 'game', name: 'Trials of Mana', igdb_game_id: 4003,
  platforms: [{ igdb_platform_id: 6, name: 'SNES', release_regions: ['korea'] }],
  matched_region: 'ja-JP',
  localizations: [{ region: 'ja-JP', translit: 'Seiken Densetsu 3' }],
}

it('keeps an unmapped-only chip bare and falls back to the matched region', async () => {
  vi.stubGlobal('fetch', vi.fn().mockResolvedValue(jsonResponse(200, { degraded: false, results: [unmappedOnlyResult] })))
  const onPick = renderPicker({ initialQuery: 'seiken' })
  const chip = await screen.findByRole('button', { name: 'Trials of Mana on SNES' })
  expect(within(chip).queryByText(/NTSC|PAL/)).not.toBeInTheDocument()
  await userEvent.click(chip)
  expect(onPick).toHaveBeenCalledWith(expect.objectContaining({ suggestedRegion: 'ntsc_j' }))
  // The key is present-but-undefined on the payload; what matters is
  // that no region list rides the pick.
  expect((onPick.mock.calls[0][0] as GamePick).regions).toBeUndefined()
})

const earliestAvailabilityResult = {
  type: 'game', name: 'Seiken Densetsu 3', igdb_game_id: 4001,
  platforms: [{ igdb_platform_id: 6, name: 'SNES', release_regions: ['japan', 'europe'] }],
}

it('pick seeds the earliest availability region when there is no matched-region suggestion', async () => {
  vi.stubGlobal('fetch', vi.fn().mockResolvedValue(jsonResponse(200, { degraded: false, results: [earliestAvailabilityResult] })))
  const onPick = renderPicker({ initialQuery: 'seiken' })
  await userEvent.click(await screen.findByRole('button', { name: 'Seiken Densetsu 3 on SNES (NTSC-J/PAL)' }))
  expect(onPick).toHaveBeenCalledWith(expect.objectContaining({ suggestedRegion: 'ntsc_j' }))
})

// The Mr. Gimmick shape: release_regions is platform-exact (no JP-twin
// fold), so a single game's platform chips can carry different
// availability and therefore different suggested regions.
const mrGimmickResult = {
  type: 'game', name: 'Mr. Gimmick', igdb_game_id: 5000,
  platforms: [
    { igdb_platform_id: 18, name: 'Famicom', release_regions: ['japan'] },
    { igdb_platform_id: 29, name: 'Sega Mega Drive', release_regions: ['europe'] },
  ],
}

it('gives each platform chip its own badges and suggested region end to end', async () => {
  vi.stubGlobal('fetch', vi.fn().mockResolvedValue(jsonResponse(200, { degraded: false, results: [mrGimmickResult] })))
  const onPick = renderPicker({ initialQuery: 'gimmick' })
  const famicomChip = await screen.findByRole('button', { name: 'Mr. Gimmick on Famicom (NTSC-J)' })
  const megaDriveChip = screen.getByRole('button', { name: 'Mr. Gimmick on Sega Mega Drive (PAL)' })
  await userEvent.click(famicomChip)
  expect(onPick).toHaveBeenLastCalledWith(expect.objectContaining({ platformName: 'Famicom', suggestedRegion: 'ntsc_j' }))
  await userEvent.click(megaDriveChip)
  expect(onPick).toHaveBeenLastCalledWith(expect.objectContaining({ platformName: 'Sega Mega Drive', suggestedRegion: 'pal' }))
})

it('searches hardware and picks a listing', async () => {
  vi.stubGlobal('fetch', vi.fn().mockResolvedValue(jsonResponse(200, {
    degraded: false,
    results: [{ type: 'hardware', name: 'Gamecube System', pc_product_id: 900, console_name: 'Gamecube', category: 'Systems' }],
  })))
  const onPick = renderPicker()
  await userEvent.click(screen.getByRole('radio', { name: /hardware/i }))
  await userEvent.type(screen.getByRole('searchbox', { name: /search/i }), 'gamecube')
  await userEvent.click(screen.getByRole('button', { name: 'Search' }))
  await userEvent.click(await screen.findByRole('button', { name: /Gamecube System/ }))
  expect(onPick).toHaveBeenCalledWith({
    kind: 'hardware', pcProductId: 900, name: 'Gamecube System', category: 'Systems',
    suggestedRegion: 'ntsc_u',
  })
  // Hardware listings ship no artwork; the row shows the type icon.
  expect(document.querySelector('svg[data-icon="console"]')).toBeInTheDocument()
})

// The region tag rides the console-name axis (productTitle.test.ts's
// consoleRegionFor table): a distinct-name JP console tags NTSC-J even
// with no "JP " prefix, and the tag rides the hardware pick as its
// suggested region.
it('tags hardware rows with the listing region and seeds the hardware pick', async () => {
  vi.stubGlobal('fetch', vi.fn().mockResolvedValue(jsonResponse(200, {
    degraded: false,
    results: [{
      type: 'hardware', name: 'Super Famicom Console', pc_product_id: 6101,
      console_name: 'Super Famicom', category: 'Systems',
    }],
  })))
  const onPick = renderPicker()
  await userEvent.click(screen.getByRole('radio', { name: /hardware/i }))
  await userEvent.type(screen.getByRole('searchbox', { name: /search/i }), 'famicom')
  await userEvent.click(screen.getByRole('button', { name: 'Search' }))
  expect(await screen.findByText('Super Famicom')).toBeInTheDocument()
  expect(screen.getByText('NTSC-J')).toBeInTheDocument()
  await userEvent.click(screen.getByRole('button', { name: 'Add Super Famicom Console' }))
  expect(onPick).toHaveBeenCalledWith({
    kind: 'hardware', pcProductId: 6101, name: 'Super Famicom Console', category: 'Systems',
    suggestedRegion: 'ntsc_j',
  })
})

it('tags a PAL pc_listing row', async () => {
  vi.stubGlobal('fetch', vi.fn().mockResolvedValue(jsonResponse(200, {
    degraded: false,
    results: [{
      type: 'pc_listing', name: 'Chrono Trigger [PAL]', pc_product_id: 7042,
      console_name: 'PAL Super Nintendo', loose_cents: 9800,
    }],
  })))
  renderPicker({ kinds: ['game', 'hardware', 'pc_listing'] })
  await userEvent.click(screen.getByRole('radio', { name: 'PriceCharting' }))
  await userEvent.type(screen.getByRole('searchbox', { name: /search/i }), 'chrono')
  await userEvent.click(screen.getByRole('button', { name: 'Search' }))
  expect(await screen.findByText('PAL Super Nintendo')).toBeInTheDocument()
  expect(screen.getByText('PAL')).toBeInTheDocument()
})

it('flags a degraded answer', async () => {
  vi.stubGlobal('fetch', vi.fn().mockResolvedValue(jsonResponse(200, { degraded: true, results: [] })))
  renderPicker()
  await userEvent.type(screen.getByRole('searchbox', { name: /search/i }), 'zzz')
  await userEvent.click(screen.getByRole('button', { name: 'Search' }))
  expect(await screen.findByRole('alert')).toHaveTextContent(/missing some results/i)
})

it('auto-runs an initial query', async () => {
  const fetchMock = vi.fn().mockResolvedValue(jsonResponse(200, gameResults))
  vi.stubGlobal('fetch', fetchMock)
  renderPicker({ initialQuery: 'chrono' })
  // The input starts prefilled, not just the query that fires.
  expect(screen.getByRole('searchbox', { name: /search/i })).toHaveValue('chrono')
  expect(await screen.findByText('Chrono Trigger')).toBeInTheDocument()
  expect(String(fetchMock.mock.calls[0][0])).toContain('q=chrono')
})

it('hides the radio fieldset entirely when only one kind is offered', () => {
  vi.stubGlobal('fetch', vi.fn())
  renderPicker({ kinds: ['pc_listing'] })
  expect(screen.queryByRole('radio')).not.toBeInTheDocument()
  expect(screen.getByRole('searchbox', { name: /search/i })).toHaveAttribute(
    'placeholder',
    'Any listing (games, variants, hardware)...',
  )
})

it('offers only Games and Hardware by default', () => {
  vi.stubGlobal('fetch', vi.fn())
  renderPicker()
  expect(screen.getByRole('radio', { name: 'Games' })).toBeInTheDocument()
  expect(screen.getByRole('radio', { name: 'Hardware' })).toBeInTheDocument()
  expect(screen.queryByRole('radio', { name: 'PriceCharting' })).not.toBeInTheDocument()
})

it('labels the search box for the default game-and-hardware kinds', () => {
  // Pinned exactly: the add wizard's tests and e2e steps locate the
  // search box by this string.
  vi.stubGlobal('fetch', vi.fn())
  renderPicker()
  expect(screen.getByRole('searchbox', { name: 'Search for games and hardware' })).toBeInTheDocument()
})

it('labels the search box for all three kinds when PriceCharting is offered', () => {
  vi.stubGlobal('fetch', vi.fn())
  renderPicker({ kinds: ['game', 'hardware', 'pc_listing'] })
  expect(
    screen.getByRole('searchbox', { name: 'Search for games, hardware, and PriceCharting' }),
  ).toBeInTheDocument()
})

it('offers PriceCharting when included in kinds, and searches it', async () => {
  const fetchMock = vi.fn().mockResolvedValue(jsonResponse(200, { degraded: false, results: [] }))
  vi.stubGlobal('fetch', fetchMock)
  renderPicker({ kinds: ['game', 'hardware', 'pc_listing'] })
  await userEvent.click(screen.getByRole('radio', { name: 'PriceCharting' }))
  await userEvent.type(screen.getByRole('searchbox', { name: /search/i }), 'mario')
  await userEvent.click(screen.getByRole('button', { name: 'Search' }))
  expect(String(fetchMock.mock.calls[0][0])).toContain('type=pc_listing&q=mario')
})

it('renders a pc_listing price line, with - for a missing value', async () => {
  vi.stubGlobal('fetch', vi.fn().mockResolvedValue(jsonResponse(200, {
    degraded: false,
    results: [
      {
        type: 'pc_listing', name: "Super Mario 64 [Player's Choice]", pc_product_id: 5099,
        console_name: 'Nintendo 64', loose_cents: 1100, cib_cents: 1760, new_cents: 3025,
      },
      {
        type: 'pc_listing', name: 'Star Fox 64', pc_product_id: 5200,
        console_name: 'Nintendo 64', loose_cents: 800, cib_cents: null, new_cents: 2000,
      },
      {
        // Real-provider listings carry a genre string as category, not a
        // hardware category - this must still render as a game, not an
        // accessory.
        type: 'pc_listing', name: 'Metroid Prime', pc_product_id: 5300,
        console_name: 'Nintendo Gamecube', category: 'Platformer',
        loose_cents: 500, cib_cents: 900, new_cents: 1500,
      },
    ],
  })))
  renderPicker({ kinds: ['game', 'hardware', 'pc_listing'] })
  await userEvent.click(screen.getByRole('radio', { name: 'PriceCharting' }))
  await userEvent.type(screen.getByRole('searchbox', { name: /search/i }), 'mario')
  await userEvent.click(screen.getByRole('button', { name: 'Search' }))

  expect(await screen.findByText("Super Mario 64 [Player's Choice]")).toBeInTheDocument()
  expect(screen.getAllByText('Nintendo 64').length).toBe(2)
  expect(screen.getByText(/Loose \$11\.00 \/ CIB \$17\.60 \/ New \$30\.25/)).toBeInTheDocument()
  expect(screen.getByText(/Loose \$8\.00 \/ CIB - \/ New \$20\.00/)).toBeInTheDocument()
  const metroidRow = screen.getByText('Metroid Prime').closest('li')!
  expect(metroidRow.querySelector('svg[data-icon="game"]')).toBeInTheDocument()
})

it('converts pc_listing quote lines into the display currency', async () => {
  vi.stubGlobal('fetch', vi.fn().mockResolvedValue(jsonResponse(200, {
    degraded: false,
    results: [{
      type: 'pc_listing', name: 'Chrono Trigger', pc_product_id: 5099,
      console_name: 'Super Nintendo', loose_cents: 1000, cib_cents: 2000, new_cents: 4000,
    }],
  })))
  renderPicker({ kinds: ['game', 'hardware', 'pc_listing'] }, { currency: 'EUR' })
  await userEvent.click(screen.getByRole('radio', { name: 'PriceCharting' }))
  await userEvent.type(screen.getByRole('searchbox', { name: /search/i }), 'chrono')
  await userEvent.click(screen.getByRole('button', { name: 'Search' }))
  expect(await screen.findByText(/Loose €5\.00 \/ CIB €10\.00 \/ New €20\.00/)).toBeInTheDocument()
})

it('picks a pc_listing result via its Use button', async () => {
  vi.stubGlobal('fetch', vi.fn().mockResolvedValue(jsonResponse(200, {
    degraded: false,
    results: [{
      type: 'pc_listing', name: "Super Mario 64 [Player's Choice]", pc_product_id: 5099,
      console_name: 'Nintendo 64', loose_cents: 1100, cib_cents: 1760, new_cents: 3025,
    }],
  })))
  const onPick = renderPicker({ kinds: ['game', 'hardware', 'pc_listing'] })
  await userEvent.click(screen.getByRole('radio', { name: 'PriceCharting' }))
  await userEvent.type(screen.getByRole('searchbox', { name: /search/i }), 'mario')
  await userEvent.click(screen.getByRole('button', { name: 'Search' }))
  await userEvent.click(await screen.findByRole('button', { name: /use super mario 64/i }))
  expect(onPick).toHaveBeenCalledWith({
    kind: 'pc_listing', pcProductId: 5099, name: "Super Mario 64 [Player's Choice]",
  })
})

it('renders a community result matching the provider idiom (name, release, tag) and emits a community pick from its platform chip', async () => {
  const results = {
    degraded: false,
    results: [
      {
        type: 'game', name: 'Repro Alpha', origin: 'community',
        product_id: 'c0ffee00-0000-4000-8000-000000000001', item_type: 'game',
        platform_name: 'SNES', first_release_date: '1994-01-01', cover_url: 'https://img.example/ra.jpg',
      },
    ],
  }
  const onPick = vi.fn()
  vi.stubGlobal('fetch', vi.fn().mockResolvedValue(jsonResponse(200, results)))
  renderPicker({ initialQuery: 'repro', onPick })
  await userEvent.click(await screen.findByRole('button', { name: 'Search' }))
  const infoLine = (await screen.findByText('Repro Alpha')).closest('p')!
  // Reads like a provider row now: name, release, tag - no bare platform text.
  expect(within(infoLine).getByText('1994')).toBeInTheDocument()
  expect(within(infoLine).getByText('community')).toBeInTheDocument()
  expect(within(infoLine).queryByText('SNES')).not.toBeInTheDocument()
  await userEvent.click(screen.getByRole('button', { name: 'Repro Alpha on SNES' }))
  expect(onPick).toHaveBeenCalledWith({
    kind: 'community', productId: 'c0ffee00-0000-4000-8000-000000000001',
    name: 'Repro Alpha', itemType: 'game', platformName: 'SNES',
    coverUrl: 'https://img.example/ra.jpg', firstReleaseDate: '1994-01-01',
  })
})

it('falls back to a plain Add button for a community result with no platform_name', async () => {
  const results = {
    degraded: false,
    results: [
      {
        type: 'game', name: 'Repro Beta', origin: 'community',
        product_id: 'c0ffee00-0000-4000-8000-000000000002', item_type: 'game',
      },
    ],
  }
  const onPick = vi.fn()
  vi.stubGlobal('fetch', vi.fn().mockResolvedValue(jsonResponse(200, results)))
  renderPicker({ initialQuery: 'repro', onPick })
  await userEvent.click(await screen.findByRole('button', { name: 'Search' }))
  await userEvent.click(await screen.findByRole('button', { name: 'Add Repro Beta' }))
  expect(onPick).toHaveBeenCalledWith({
    kind: 'community', productId: 'c0ffee00-0000-4000-8000-000000000002',
    name: 'Repro Beta', itemType: 'game', platformName: undefined,
  })
})

it('tags a community row with a known region and carries it on the pick', async () => {
  const results = {
    degraded: false,
    results: [
      {
        type: 'game', name: 'Repro Gamma', origin: 'community',
        product_id: 'c0ffee00-0000-4000-8000-000000000003', item_type: 'game',
        platform_name: 'SNES', region: 'ntsc_j',
      },
    ],
  }
  const onPick = vi.fn()
  vi.stubGlobal('fetch', vi.fn().mockResolvedValue(jsonResponse(200, results)))
  renderPicker({ initialQuery: 'repro', onPick })
  await userEvent.click(await screen.findByRole('button', { name: 'Search' }))
  const infoLine = (await screen.findByText('Repro Gamma')).closest('p')!
  expect(within(infoLine).getByText('NTSC-J')).toBeInTheDocument()
  await userEvent.click(screen.getByRole('button', { name: 'Repro Gamma on SNES' }))
  expect(onPick).toHaveBeenCalledWith(expect.objectContaining({ region: 'ntsc_j' }))
})

// Community region is open-world (curated free text, not just the four
// known values) - an unrecognized region rides the row verbatim rather
// than disappearing or throwing, same as regionLabelText's contract.
it('tags a community row with an open-world region verbatim', async () => {
  const results = {
    degraded: false,
    results: [
      {
        type: 'game', name: 'Repro Delta', origin: 'community',
        product_id: 'c0ffee00-0000-4000-8000-000000000004', item_type: 'game',
        platform_name: 'SNES', region: 'Korea',
      },
    ],
  }
  vi.stubGlobal('fetch', vi.fn().mockResolvedValue(jsonResponse(200, results)))
  renderPicker({ initialQuery: 'repro' })
  await userEvent.click(await screen.findByRole('button', { name: 'Search' }))
  const infoLine = (await screen.findByText('Repro Delta')).closest('p')!
  expect(within(infoLine).getByText('Korea')).toBeInTheDocument()
})

it('hides community results when communityLane is hidden', async () => {
  const results = {
    degraded: false,
    results: [
      { type: 'game', name: 'Repro Alpha', origin: 'community', product_id: 'c0ffee00-0000-4000-8000-000000000001', item_type: 'game', platform_name: 'SNES' },
    ],
  }
  vi.stubGlobal('fetch', vi.fn().mockResolvedValue(jsonResponse(200, results)))
  renderPicker({ initialQuery: 'repro', communityLane: 'hidden' })
  await userEvent.click(await screen.findByRole('button', { name: 'Search' }))
  expect(await screen.findByText(/no results for/i)).toBeInTheDocument()
  expect(screen.queryByRole('button', { name: 'Repro Alpha on SNES' })).not.toBeInTheDocument()
})

it('seeds from initialState, auto-runs it, and reports state changes', async () => {
  const fetchMock = vi.fn().mockResolvedValue(jsonResponse(200, gameResults))
  vi.stubGlobal('fetch', fetchMock)
  const onStateChange = vi.fn()
  renderPicker({
    initialState: { kind: 'game', text: 'chrono', submitted: 'chrono' },
    onStateChange,
  })
  expect(screen.getByRole('searchbox', { name: /search/i })).toHaveValue('chrono')
  expect(await screen.findByText('Chrono Trigger')).toBeInTheDocument()
  await userEvent.type(screen.getByRole('searchbox', { name: /search/i }), 'x')
  expect(onStateChange).toHaveBeenLastCalledWith({ kind: 'game', text: 'chronox', submitted: 'chrono' })
  await userEvent.click(screen.getByRole('button', { name: 'Search' }))
  expect(onStateChange).toHaveBeenLastCalledWith({ kind: 'game', text: 'chronox', submitted: 'chronox' })
})

it('initialState wins over initialQuery', () => {
  vi.stubGlobal('fetch', vi.fn().mockResolvedValue(jsonResponse(200, gameResults)))
  renderPicker({ initialQuery: 'zelda', initialState: { kind: 'game', text: 'chrono', submitted: 'chrono' } })
  expect(screen.getByRole('searchbox', { name: /search/i })).toHaveValue('chrono')
})

it('reports a kind switch', async () => {
  vi.stubGlobal('fetch', vi.fn().mockResolvedValue(jsonResponse(200, { degraded: false, results: [] })))
  const onStateChange = vi.fn()
  renderPicker({ onStateChange })
  await userEvent.click(screen.getByRole('radio', { name: 'Hardware' }))
  expect(onStateChange).toHaveBeenLastCalledWith({ kind: 'hardware', text: '', submitted: '' })
})
