import { screen, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { jsonResponse } from '../../test/fixtures'
import { renderWithMoney } from '../../test/money'
import SearchPicker from './SearchPicker'

function renderPicker(
  props: Partial<Parameters<typeof SearchPicker>[0]> = {},
  opts: { currency?: string } = {},
) {
  const onPick = vi.fn()
  renderWithMoney(<SearchPicker onPick={onPick} {...props} />, opts)
  return onPick
}

afterEach(() => vi.unstubAllGlobals())

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
  })
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
  })
  // Hardware listings ship no artwork; the row shows the type icon.
  expect(document.querySelector('svg[data-icon="console"]')).toBeInTheDocument()
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
