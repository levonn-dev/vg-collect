import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { render, screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { jsonResponse } from '../../test/fixtures'
import SearchPicker from './SearchPicker'

function renderPicker(props: Partial<Parameters<typeof SearchPicker>[0]> = {}) {
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } })
  const onPick = vi.fn()
  render(
    <QueryClientProvider client={qc}>
      <SearchPicker onPick={onPick} {...props} />
    </QueryClientProvider>,
  )
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
})

it('flags a degraded answer', async () => {
  vi.stubGlobal('fetch', vi.fn().mockResolvedValue(jsonResponse(200, { degraded: true, results: [] })))
  renderPicker()
  await userEvent.type(screen.getByRole('searchbox', { name: /search/i }), 'zzz')
  await userEvent.click(screen.getByRole('button', { name: 'Search' }))
  expect(await screen.findByRole('alert')).toHaveTextContent(/catalog search is degraded/i)
})

it('auto-runs an initial query', async () => {
  const fetchMock = vi.fn().mockResolvedValue(jsonResponse(200, gameResults))
  vi.stubGlobal('fetch', fetchMock)
  renderPicker({ initialQuery: 'chrono' })
  expect(await screen.findByText('Chrono Trigger')).toBeInTheDocument()
})
