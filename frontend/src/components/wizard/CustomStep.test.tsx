import { i18n } from '@lingui/core'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { cleanup, fireEvent, screen } from '@testing-library/react'
import type { ReactElement } from 'react'
import { messages as jaMessages } from '../../locales/ja.po'
import { fxRatesFixture, jsonResponse, meFixture } from '../../test/fixtures'
import { renderWithI18n as renderI18n } from '../../test/i18n'
import CustomStep from './CustomStep'

// PlatformPicker reads the /api/platforms cache through useQuery, which
// needs a QueryClientProvider ancestor - renderWithI18n alone (no query
// client) is what every other CustomStep prop needs, so this wraps it
// rather than repeating the QueryClientProvider boilerplate per test.
// The catalog mirrors PlatformPicker.test.tsx's fixture, extended with
// Super Famicom (igdb_id 58) so a region-platform-default test has a
// JP-market platform to pick. staleTime Infinity plus the seeded me/fx
// queries keep the embedded SearchPicker's unconditional useDisplayMoney
// call from ever reaching the fetch mock - the same convention
// AddWizard.test.tsx's renderWizard uses.
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
  // Order matters: cleanup() before activate() - see EntryDetail.test.tsx's
  // afterEach for why (I18nProvider update outside act otherwise).
  cleanup()
  i18n.activate('en')
})

test('region field present and free text allowed', () => {
  const onNext = vi.fn()
  renderWithI18n(<CustomStep onBack={() => {}} onNext={onNext} />)
  fireEvent.change(screen.getByLabelText('Name'), { target: { value: 'Zero Tolerance Link Cable' } })
  fireEvent.click(screen.getByRole('button', { name: "My region isn't listed" }))
  fireEvent.change(screen.getByLabelText('Region'), { target: { value: 'World' } })
  fireEvent.click(screen.getByRole('button', { name: 'Continue' }))
  expect(onNext).toHaveBeenCalledWith(expect.objectContaining({ region: 'World' }))
})

// Unlike DetailsStep/EntryForm, an empty region here legitimately
// means "decide at details" - CustomStep must not force a pick.
test('region control is not required', () => {
  renderWithI18n(<CustomStep onBack={() => {}} onNext={() => {}} />)
  expect(screen.getByLabelText('Region')).not.toBeRequired()
})

test('region-specific platform defaults a pristine region', async () => {
  renderWithI18n(<CustomStep onBack={() => {}} onNext={() => {}} />)
  // pick Super Famicom through the PlatformPicker (its catalog is the
  // stubbed /api/platforms fixture used by the existing picker tests)
  fireEvent.change(screen.getByLabelText('Platform'), { target: { value: 'super fam' } })
  fireEvent.click(await screen.findByRole('button', { name: 'Super Famicom' }))
  expect(screen.getByLabelText('Region')).toHaveValue('ntsc_j')
})

test('an explicit region choice survives a platform pick', async () => {
  renderWithI18n(<CustomStep onBack={() => {}} onNext={() => {}} />)
  fireEvent.change(screen.getByLabelText('Region'), { target: { value: 'pal' } })
  fireEvent.change(screen.getByLabelText('Platform'), { target: { value: 'super fam' } })
  fireEvent.click(await screen.findByRole('button', { name: 'Super Famicom' }))
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
  fireEvent.change(screen.getByLabelText('Platform'), { target: { value: 'super fam' } })
  fireEvent.click(await screen.findByRole('button', { name: 'Super Famicom' }))
  expect(screen.getByLabelText('Region')).toHaveValue('pal')
})

test('seed fills name and type when fresh', () => {
  renderWithI18n(<CustomStep seed={{ displayName: 'link cable', itemType: 'accessory' }} onBack={() => {}} onNext={() => {}} />)
  expect(screen.getByLabelText('Name')).toHaveValue('link cable')
  expect(screen.getByLabelText('Item type')).toHaveValue('accessory')
})

// Item type options used to be raw, untranslated text (a straight
// "game"/"console"/"accessory" <option> literal) - this pins that they
// now render through itemTypeWireLabels, so a ja reader sees ja text
// rather than the English wire value. Options are found by their
// locale-invariant value (not the label text, which is itself
// translated under ja) so the query does not depend on the very
// translation being pinned.
test('item type options render the translated wire label under ja', () => {
  i18n.load('ja', jaMessages)
  i18n.activate('ja')
  renderWithI18n(<CustomStep onBack={() => {}} onNext={() => {}} />)
  // Found by the option's value attribute (locale-invariant) rather
  // than getByDisplayValue, which matches a select's VISIBLE option
  // text - itself translated under ja, so it cannot locate the node.
  const select = document.querySelector('option[value="game"]')!.parentElement!
  const texts = Array.from(select.querySelectorAll('option')).map((o) => o.textContent)
  expect(texts).toEqual(['ゲーム', 'ゲーム機', '周辺機器'])
})

// The platform carries no release_regions of its own, so the chip's
// suggestion falls all the way back to the matched-region mapping alone
// (ja-JP -> ntsc_j) - the same fallback SearchPicker.test.tsx's
// "unmapped-only" case exercises for the raw suggestion; this fixture
// carries it through a based-add end to end.
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
  fireEvent.click(screen.getByRole('button', { name: 'Base on an existing item' }))
  // SearchPicker mounts embedded; drive it with this file's own
  // search-stub fixture (same fetch-stub idiom as AddWizard.test.tsx).
  fireEvent.change(screen.getByRole('searchbox'), { target: { value: 'card fighters' } })
  fireEvent.click(screen.getByRole('button', { name: 'Search' }))
  fireEvent.click(await screen.findByRole('button', { name: /SNK vs\. Capcom/ }))
  expect(screen.getByLabelText('Name')).toHaveValue('SNK vs. Capcom: Card Fighters 2')
  expect(screen.getByLabelText('Region')).toHaveValue('ntsc_j')
  expect(screen.getByLabelText(/release date/i)).toHaveValue('1999-12-16')
  expect(screen.getByLabelText(/cover image link/i)).toHaveValue('https://img.example/cf2.jpg')
  expect(screen.queryByRole('searchbox')).not.toBeInTheDocument() // picker closed
})

// Pins the remount fix: opening the escape hatch first puts
// RegionPicker in free-text mode, and a based-add whose pick carries a
// known suggestedRegion must still land on the SELECT (not leave the
// stale free-text input showing the wire value), because the remount
// re-derives display mode from the freshly applied region.
test('based-add remounts the region control into select mode after the escape hatch was open', async () => {
  vi.stubGlobal('fetch', vi.fn().mockResolvedValue(jsonResponse(200, cardFightersAnswer)))
  renderWithI18n(<CustomStep onBack={() => {}} onNext={() => {}} />)
  fireEvent.click(screen.getByRole('button', { name: "My region isn't listed" }))
  expect(screen.getByLabelText('Region').tagName).toBe('INPUT')
  fireEvent.click(screen.getByRole('button', { name: 'Base on an existing item' }))
  fireEvent.change(screen.getByRole('searchbox'), { target: { value: 'card fighters' } })
  fireEvent.click(screen.getByRole('button', { name: 'Search' }))
  fireEvent.click(await screen.findByRole('button', { name: /SNK vs\. Capcom/ }))
  const region = screen.getByLabelText('Region')
  expect(region.tagName).toBe('SELECT')
  expect(region).toHaveValue('ntsc_j')
})

// Super Famicom is a Systems-category listing - the sharper pin for the
// category rule, since it exercises the console arm (everything else
// hardware lists is an accessory). Same fixture shape as SearchPicker.test.tsx's
// "tags hardware rows..." case, so consoleRegionFor resolves ntsc_j the
// same way.
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
  fireEvent.click(screen.getByRole('button', { name: 'Base on an existing item' }))
  fireEvent.click(screen.getByRole('radio', { name: /hardware/i }))
  fireEvent.change(screen.getByRole('searchbox'), { target: { value: 'super famicom' } })
  fireEvent.click(screen.getByRole('button', { name: 'Search' }))
  fireEvent.click(await screen.findByRole('button', { name: 'Add Super Famicom Console' }))
  expect(screen.getByLabelText('Name')).toHaveValue('Super Famicom Console')
  expect(screen.getByLabelText('Item type')).toHaveValue('console') // Systems -> console
  expect(screen.getByLabelText('Region')).toHaveValue('ntsc_j')
  // platformName/platformIgdbId both reset for hardware; PlatformPicker's
  // own search input is empty either way, pinning platformIgdbId stayed
  // undefined (a real id would flip it into its confirmed-pick display,
  // which carries no labeled control at all).
  expect(screen.getByLabelText('Platform')).toHaveValue('')
  expect(screen.queryByRole('searchbox')).not.toBeInTheDocument()
})

// Carries a platform_name, a release date, and cover art so all three
// community-only fields (platformName plus coverUrl and firstReleaseDate,
// the based-add prefill fields GamePick and CommunityPick share) are
// exercised in one fixture; itemType 'accessory' (rather than 'game',
// already covered by the other two arms) pins that itemType really rides
// the pick, not a hardcoded default.
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
  fireEvent.click(screen.getByRole('button', { name: 'Base on an existing item' }))
  fireEvent.change(screen.getByRole('searchbox'), { target: { value: 'repro gamma' } })
  fireEvent.click(screen.getByRole('button', { name: 'Search' }))
  fireEvent.click(await screen.findByRole('button', { name: 'Repro Gamma on Game Boy' }))
  expect(screen.getByLabelText('Name')).toHaveValue('Repro Gamma')
  expect(screen.getByLabelText('Item type')).toHaveValue('accessory')
  // The keyed remount re-derives PlatformPicker's display mode from
  // this pick's facts, so its free-text field visibly shows the
  // community platformName - not just the leftover search text a
  // stale, unremounted picker would otherwise still be caching.
  expect(screen.getByLabelText('Platform')).toHaveValue('Game Boy')
  expect(screen.getByLabelText('Region')).toHaveValue('') // this fixture's community row carries no region; see the region-filling case below
  expect(screen.getByLabelText(/release date/i)).toHaveValue('2001-05-01')
  expect(screen.getByLabelText(/cover image link/i)).toHaveValue('https://img.example/gamma.jpg')
  expect(screen.queryByRole('searchbox')).not.toBeInTheDocument()
  fireEvent.click(screen.getByRole('button', { name: 'Continue' }))
  expect(onNext).toHaveBeenCalledWith(expect.objectContaining({ platformName: 'Game Boy', platformIgdbId: undefined }))
})

// Same shape as reproGammaAnswer, but this result carries a region -
// applyBase's community arm (region: p.region ?? '') must carry it
// into the form, the same as suggestedRegion does for game/hardware.
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
  fireEvent.click(screen.getByRole('button', { name: 'Base on an existing item' }))
  fireEvent.change(screen.getByRole('searchbox'), { target: { value: 'repro delta' } })
  fireEvent.click(screen.getByRole('button', { name: 'Search' }))
  fireEvent.click(await screen.findByRole('button', { name: 'Repro Delta on Game Boy' }))
  expect(screen.getByLabelText('Region')).toHaveValue('pal')
  expect(screen.getByLabelText('Developers 1')).toHaveValue('Garage Team')
  expect(screen.getByLabelText('Publishers 1')).toHaveValue('Repro House')
  fireEvent.click(screen.getByRole('button', { name: 'Continue' }))
  expect(onNext).toHaveBeenCalledWith(expect.objectContaining({
    developers: ['Garage Team'], publishers: ['Repro House'],
  }))
})

// Cancel must be a pure escape hatch, never a side door that clears
// or replaces whatever the user already typed.
test('Cancel closes the picker and leaves the form untouched', async () => {
  vi.stubGlobal('fetch', vi.fn().mockResolvedValue(jsonResponse(200, { degraded: false, results: [] })))
  renderWithI18n(<CustomStep onBack={() => {}} onNext={() => {}} />)
  fireEvent.change(screen.getByLabelText('Name'), { target: { value: 'Untouched Name' } })
  fireEvent.click(screen.getByRole('button', { name: 'Base on an existing item' }))
  // Name was already typed, so SearchPicker's initialQuery auto-runs a
  // search on mount; let it settle before Cancel so no fetch promise is
  // still in flight when the picker unmounts.
  await screen.findByText(/no results/i)
  fireEvent.click(screen.getByRole('button', { name: 'Cancel' }))
  expect(screen.queryByRole('searchbox')).not.toBeInTheDocument()
  expect(screen.getByLabelText('Name')).toHaveValue('Untouched Name')
  expect(screen.getByRole('button', { name: 'Base on an existing item' })).toBeInTheDocument()
})

// SearchPicker's own search form must not sit inside CustomStep's form
// element, or a native 'submit' bubbles up from the nested form to the
// outer one (nesting <form> is invalid markup but React's direct DOM
// construction does not enforce that, so the bug is real, not
// theoretical) and Search would double as an early Continue. This
// pins the fix: a Name typed before opening the picker must survive a
// picker search untouched by the wizard's own onNext.
test('opening the picker and searching does not submit the outer form early', async () => {
  const onNext = vi.fn()
  // mockImplementation, not mockResolvedValue: a Response body can only
  // be read once, and a Name typed before opening the picker seeds
  // SearchPicker's initialQuery, so it auto-runs a first search before
  // the explicit one this test drives - two reads of the same fetch
  // mock (see AddWizard.test.tsx's "prefers the wizard snapshot..." test).
  vi.stubGlobal('fetch', vi.fn().mockImplementation(() => Promise.resolve(jsonResponse(200, { degraded: false, results: [] }))))
  renderWithI18n(<CustomStep onBack={() => {}} onNext={onNext} />)
  fireEvent.change(screen.getByLabelText('Name'), { target: { value: 'Working title' } })
  fireEvent.click(screen.getByRole('button', { name: 'Base on an existing item' }))
  fireEvent.change(screen.getByRole('searchbox'), { target: { value: 'anything' } })
  fireEvent.click(screen.getByRole('button', { name: 'Search' }))
  await screen.findByText(/no results/i)
  expect(onNext).not.toHaveBeenCalled()
})
