import { i18n } from '@lingui/core'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { cleanup, screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import type { ReactElement } from 'react'
import { messages as jaMessages } from '../../locales/ja.po'
import { fxRatesFixture, jsonResponse, meFixture } from '../../test/fixtures'
import { renderWithI18n as renderI18n } from '../../test/i18n'
import CustomStep from './CustomStep'

// PlatformPicker needs a QueryClientProvider ancestor renderWithI18n alone
// doesn't provide. Super Famicom (igdb_id 58) gives a region-platform-default
// test a JP-market platform. staleTime Infinity plus seeded me/fx keep
// SearchPicker's useDisplayMoney from reaching the fetch mock.
const catalog = {
  platforms: [
    { igdb_id: 19, name: 'Super Nintendo Entertainment System', aliases: ['snes', 'super nintendo'] },
    { igdb_id: 18, name: 'Nintendo Entertainment System', aliases: ['nes'] },
    { igdb_id: 58, name: 'Super Famicom', aliases: ['sfc'] },
  ],
}

function renderWithI18n(ui: ReactElement) {
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false, staleTime: Infinity } } })
  qc.setQueryData(['platforms'], catalog)
  qc.setQueryData(['me'], meFixture())
  qc.setQueryData(['fx'], fxRatesFixture())
  return renderI18n(<QueryClientProvider client={qc}>{ui}</QueryClientProvider>)
}

afterEach(() => {
  vi.unstubAllGlobals()
  // cleanup() before activate(): otherwise I18nProvider updates outside act.
  cleanup()
  i18n.activate('en')
})

test('region field present and free text allowed', async () => {
  const onNext = vi.fn()
  renderWithI18n(<CustomStep onBack={() => {}} onNext={onNext} />)
  await userEvent.type(screen.getByLabelText('Name'), 'Zero Tolerance Link Cable')
  await userEvent.click(screen.getByRole('button', { name: "My region isn't listed" }))
  await userEvent.type(screen.getByLabelText('Region'), 'World')
  await userEvent.click(screen.getByRole('button', { name: 'Continue' }))
  expect(onNext).toHaveBeenCalledWith(expect.objectContaining({ region: 'World' }))
})

// Unlike DetailsStep/EntryForm, an empty region legitimately means "decide at details".
test('region control is not required', () => {
  renderWithI18n(<CustomStep onBack={() => {}} onNext={() => {}} />)
  expect(screen.getByLabelText('Region')).not.toBeRequired()
})

test('region-specific platform defaults a pristine region', async () => {
  renderWithI18n(<CustomStep onBack={() => {}} onNext={() => {}} />)
  // Catalog is the stubbed /api/platforms fixture the existing picker tests use.
  await userEvent.type(screen.getByLabelText('Platform'), 'super fam')
  await userEvent.click(await screen.findByRole('button', { name: 'Super Famicom' }))
  expect(screen.getByLabelText('Region')).toHaveValue('ntsc_j')
})

test('an explicit region choice survives a platform pick', async () => {
  renderWithI18n(<CustomStep onBack={() => {}} onNext={() => {}} />)
  await userEvent.selectOptions(screen.getByLabelText('Region'), 'pal')
  await userEvent.type(screen.getByLabelText('Platform'), 'super fam')
  await userEvent.click(await screen.findByRole('button', { name: 'Super Famicom' }))
  expect(screen.getByLabelText('Region')).toHaveValue('pal')
})

test('an initialValues region survives a Back remount platform pick', async () => {
  renderWithI18n(
    <CustomStep
      initialValues={{
        displayName: 'Chrono Trigger Repro', itemType: 'game', platformName: '',
        platformIgdbId: undefined, region: 'pal', firstReleaseDate: '', coverUrl: '',
        developers: [], publishers: [],
      }}
      onBack={() => {}}
      onNext={() => {}}
    />,
  )
  await userEvent.type(screen.getByLabelText('Platform'), 'super fam')
  await userEvent.click(await screen.findByRole('button', { name: 'Super Famicom' }))
  expect(screen.getByLabelText('Region')).toHaveValue('pal')
})

test('seed fills name and type when fresh', () => {
  renderWithI18n(<CustomStep seed={{ displayName: 'link cable', itemType: 'accessory' }} onBack={() => {}} onNext={() => {}} />)
  expect(screen.getByLabelText('Name')).toHaveValue('link cable')
  expect(screen.getByLabelText('Item type')).toHaveValue('accessory')
})

// Pins that Type options render through itemTypeWireLabels (ja text, not the
// English wire value). Found by locale-invariant value, not label text, so
// the query doesn't depend on the translation being pinned.
test('item type options render the translated wire label under ja', () => {
  i18n.load('ja', jaMessages)
  i18n.activate('ja')
  renderWithI18n(<CustomStep onBack={() => {}} onNext={() => {}} />)
  // getByDisplayValue matches visible (translated) text, so the value
  // attribute locates the node under ja instead.
  const select = document.querySelector('option[value="game"]')!.parentElement!
  const texts = Array.from(select.querySelectorAll('option')).map((o) => o.textContent)
  expect(texts).toEqual(['ゲーム', 'ゲーム機', '周辺機器'])
})

// No release_regions, so the suggestion falls back to the matched-region
// mapping alone (ja-JP -> ntsc_j); carries that through a based-add end to end.
const cardFightersAnswer = {
  degraded: false,
  results: [{
    type: 'game', name: 'SNK vs. Capcom: Card Fighters 2', igdb_game_id: 9001,
    first_release_date: '1999-12-16', cover_url: 'https://img.example/cf2.jpg',
    matched_region: 'ja-JP',
    platforms: [{ igdb_platform_id: 120, name: 'Neo Geo Pocket Color' }],
  }],
}

test('base on an existing item fills the form from a game pick', async () => {
  vi.stubGlobal('fetch', vi.fn().mockResolvedValue(jsonResponse(200, cardFightersAnswer)))
  renderWithI18n(<CustomStep onBack={() => {}} onNext={() => {}} />)
  await userEvent.click(screen.getByRole('button', { name: 'Base on an existing item' }))
  // SearchPicker mounts embedded; drive it with this file's own search-stub fixture.
  await userEvent.type(screen.getByRole('searchbox'), 'card fighters')
  await userEvent.click(screen.getByRole('button', { name: 'Search' }))
  await userEvent.click(await screen.findByRole('button', { name: /SNK vs\. Capcom/ }))
  expect(screen.getByLabelText('Name')).toHaveValue('SNK vs. Capcom: Card Fighters 2')
  expect(screen.getByLabelText('Region')).toHaveValue('ntsc_j')
  expect(screen.getByLabelText(/release date/i)).toHaveValue('1999-12-16')
  expect(screen.getByLabelText(/cover image link/i)).toHaveValue('https://img.example/cf2.jpg')
  expect(screen.queryByRole('searchbox')).not.toBeInTheDocument() // picker closed
})

// Opening the escape hatch puts RegionPicker in free-text mode; a based-add
// with a known suggestedRegion must still land on the SELECT, since the
// remount re-derives display mode from the freshly applied region.
test('based-add remounts the region control into select mode after the escape hatch was open', async () => {
  vi.stubGlobal('fetch', vi.fn().mockResolvedValue(jsonResponse(200, cardFightersAnswer)))
  renderWithI18n(<CustomStep onBack={() => {}} onNext={() => {}} />)
  await userEvent.click(screen.getByRole('button', { name: "My region isn't listed" }))
  expect(screen.getByLabelText('Region').tagName).toBe('INPUT')
  await userEvent.click(screen.getByRole('button', { name: 'Base on an existing item' }))
  await userEvent.type(screen.getByRole('searchbox'), 'card fighters')
  await userEvent.click(screen.getByRole('button', { name: 'Search' }))
  await userEvent.click(await screen.findByRole('button', { name: /SNK vs\. Capcom/ }))
  const region = screen.getByLabelText('Region')
  expect(region.tagName).toBe('SELECT')
  expect(region).toHaveValue('ntsc_j')
})

// Systems-category listing exercises the console arm of the category rule
// (everything else hardware lists is accessory); consoleRegionFor resolves ntsc_j.
const superFamicomAnswer = {
  degraded: false,
  results: [{
    type: 'hardware', name: 'Super Famicom Console', pc_product_id: 6101,
    console_name: 'Super Famicom', category: 'Systems',
  }],
}

test('base on an existing item fills the form from a hardware pick', async () => {
  vi.stubGlobal('fetch', vi.fn().mockResolvedValue(jsonResponse(200, superFamicomAnswer)))
  renderWithI18n(<CustomStep onBack={() => {}} onNext={() => {}} />)
  await userEvent.click(screen.getByRole('button', { name: 'Base on an existing item' }))
  await userEvent.click(screen.getByRole('radio', { name: /hardware/i }))
  await userEvent.type(screen.getByRole('searchbox'), 'super famicom')
  await userEvent.click(screen.getByRole('button', { name: 'Search' }))
  await userEvent.click(await screen.findByRole('button', { name: 'Add Super Famicom Console' }))
  expect(screen.getByLabelText('Name')).toHaveValue('Super Famicom Console')
  expect(screen.getByLabelText('Item type')).toHaveValue('console') // Systems -> console
  expect(screen.getByLabelText('Region')).toHaveValue('ntsc_j')
  // platformName/platformIgdbId both reset for hardware; empty search input
  // pins platformIgdbId stayed undefined (a real id flips to confirmed-pick
  // display with no labeled control).
  expect(screen.getByLabelText('Platform')).toHaveValue('')
  expect(screen.queryByRole('searchbox')).not.toBeInTheDocument()
})

// Exercises platformName/coverUrl/firstReleaseDate (fields GamePick and
// CommunityPick share) in one fixture; itemType 'accessory' (not 'game',
// already covered) pins that itemType rides the pick, not a default.
const reproGammaAnswer = {
  degraded: false,
  results: [{
    type: 'game', name: 'Repro Gamma', origin: 'community',
    product_id: 'c0ffee00-0000-4000-8000-000000000099', item_type: 'accessory',
    platform_name: 'Game Boy', first_release_date: '2001-05-01', cover_url: 'https://img.example/gamma.jpg',
  }],
}

test('base on an existing item fills the form from a community pick', async () => {
  const onNext = vi.fn()
  vi.stubGlobal('fetch', vi.fn().mockResolvedValue(jsonResponse(200, reproGammaAnswer)))
  renderWithI18n(<CustomStep onBack={() => {}} onNext={onNext} />)
  await userEvent.click(screen.getByRole('button', { name: 'Base on an existing item' }))
  await userEvent.type(screen.getByRole('searchbox'), 'repro gamma')
  await userEvent.click(screen.getByRole('button', { name: 'Search' }))
  await userEvent.click(await screen.findByRole('button', { name: 'Repro Gamma on Game Boy' }))
  expect(screen.getByLabelText('Name')).toHaveValue('Repro Gamma')
  expect(screen.getByLabelText('Item type')).toHaveValue('accessory')
  // Keyed remount re-derives PlatformPicker's mode from the pick's facts, so
  // the free-text field shows the community platformName, not stale search text.
  expect(screen.getByLabelText('Platform')).toHaveValue('Game Boy')
  expect(screen.getByLabelText('Region')).toHaveValue('') // this fixture's community row carries no region; see the region-filling case below
  expect(screen.getByLabelText(/release date/i)).toHaveValue('2001-05-01')
  expect(screen.getByLabelText(/cover image link/i)).toHaveValue('https://img.example/gamma.jpg')
  expect(screen.queryByRole('searchbox')).not.toBeInTheDocument()
  await userEvent.click(screen.getByRole('button', { name: 'Continue' }))
  expect(onNext).toHaveBeenCalledWith(expect.objectContaining({ platformName: 'Game Boy', platformIgdbId: undefined }))
})

// Same shape as reproGammaAnswer but with a region: applyBase's community
// arm must carry it into the form, like suggestedRegion does for game/hardware.
const reproDeltaAnswer = {
  degraded: false,
  results: [{
    type: 'game', name: 'Repro Delta', origin: 'community',
    product_id: 'c0ffee00-0000-4000-8000-0000000000aa', item_type: 'game',
    platform_name: 'Game Boy', region: 'pal',
    developers: ['Garage Team'], publishers: ['Repro House'],
  }],
}

test('base on an existing item fills region and credits from a community pick that carries them', async () => {
  const onNext = vi.fn()
  vi.stubGlobal('fetch', vi.fn().mockResolvedValue(jsonResponse(200, reproDeltaAnswer)))
  renderWithI18n(<CustomStep onBack={() => {}} onNext={onNext} />)
  await userEvent.click(screen.getByRole('button', { name: 'Base on an existing item' }))
  await userEvent.type(screen.getByRole('searchbox'), 'repro delta')
  await userEvent.click(screen.getByRole('button', { name: 'Search' }))
  await userEvent.click(await screen.findByRole('button', { name: 'Repro Delta on Game Boy' }))
  expect(screen.getByLabelText('Region')).toHaveValue('pal')
  expect(screen.getByLabelText('Developers: 1')).toHaveValue('Garage Team')
  expect(screen.getByLabelText('Publishers: 1')).toHaveValue('Repro House')
  await userEvent.click(screen.getByRole('button', { name: 'Continue' }))
  expect(onNext).toHaveBeenCalledWith(expect.objectContaining({
    developers: ['Garage Team'], publishers: ['Repro House'],
  }))
})

// Cancel must be a pure escape hatch, never clearing/replacing what the user already typed.
test('Cancel closes the picker and leaves the form untouched', async () => {
  vi.stubGlobal('fetch', vi.fn().mockResolvedValue(jsonResponse(200, { degraded: false, results: [] })))
  renderWithI18n(<CustomStep onBack={() => {}} onNext={() => {}} />)
  await userEvent.type(screen.getByLabelText('Name'), 'Untouched Name')
  await userEvent.click(screen.getByRole('button', { name: 'Base on an existing item' }))
  // Name was already typed, so initialQuery auto-runs a search; let it settle
  // before Cancel so no fetch promise is in flight when the picker unmounts.
  await screen.findByText(/no results/i)
  await userEvent.click(screen.getByRole('button', { name: 'Cancel' }))
  expect(screen.queryByRole('searchbox')).not.toBeInTheDocument()
  expect(screen.getByLabelText('Name')).toHaveValue('Untouched Name')
  expect(screen.getByRole('button', { name: 'Base on an existing item' })).toBeInTheDocument()
})

// Nested <form> is invalid markup but React's DOM construction doesn't
// enforce it, so a native submit from SearchPicker's form could really bubble
// to CustomStep's, making Search double as an early Continue.
test('opening the picker and searching does not submit the outer form early', async () => {
  const onNext = vi.fn()
  // mockImplementation, not mockResolvedValue: a Response body reads once, and
  // initialQuery auto-runs a first search before the explicit one this test
  // drives - two reads of the same mock.
  vi.stubGlobal('fetch', vi.fn().mockImplementation(() => Promise.resolve(jsonResponse(200, { degraded: false, results: [] }))))
  renderWithI18n(<CustomStep onBack={() => {}} onNext={onNext} />)
  await userEvent.type(screen.getByLabelText('Name'), 'Working title')
  await userEvent.click(screen.getByRole('button', { name: 'Base on an existing item' }))
  // searchbox opens prefilled from Name; clear before typing the distinct query.
  const searchbox = screen.getByRole('searchbox')
  await userEvent.clear(searchbox)
  await userEvent.type(searchbox, 'anything')
  await userEvent.click(screen.getByRole('button', { name: 'Search' }))
  await screen.findByText(/no results/i)
  expect(onNext).not.toHaveBeenCalled()
})
