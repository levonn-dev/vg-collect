import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { render, screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { jsonResponse } from '../../test/fixtures'
import ProxyPicker from './ProxyPicker'

afterEach(() => vi.unstubAllGlobals())

it('search, platform pick, resolve, and hand back the product', async () => {
  const product = { id: 'p9', type: 'game', name: 'Chrono Trigger', created_at: 'x', updated_at: 'x' }
  const fetchMock = vi.fn().mockImplementation((url: string) => {
    const u = String(url)
    if (u.startsWith('/api/search')) {
      return Promise.resolve(jsonResponse(200, {
        degraded: false,
        results: [{ type: 'game', name: 'Chrono Trigger', igdb_game_id: 1000, platforms: [{ igdb_platform_id: 6, name: 'SNES' }] }],
      }))
    }
    if (u === '/api/products/resolve') return Promise.resolve(jsonResponse(200, product))
    return Promise.resolve(jsonResponse(404, {}))
  })
  vi.stubGlobal('fetch', fetchMock)
  const onPick = vi.fn()
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } })
  render(
    <QueryClientProvider client={qc}>
      <ProxyPicker onPick={onPick} onClose={vi.fn()} />
    </QueryClientProvider>,
  )
  await userEvent.type(screen.getByRole('searchbox', { name: /search/i }), 'chrono')
  await userEvent.click(screen.getByRole('button', { name: 'Search' }))
  await userEvent.click(await screen.findByRole('button', { name: 'Chrono Trigger on SNES' }))
  expect(onPick).toHaveBeenCalledWith(expect.objectContaining({ id: 'p9' }))
})

it('offers PriceCharting and resolves a pc_listing pick to its product', async () => {
  const product = { id: 'p10', type: 'pc_listing', name: "Super Mario 64 [Player's Choice]", created_at: 'x', updated_at: 'x' }
  const fetchMock = vi.fn().mockImplementation((url: string) => {
    const u = String(url)
    if (u.startsWith('/api/search')) {
      return Promise.resolve(jsonResponse(200, {
        degraded: false,
        results: [{
          type: 'pc_listing', name: "Super Mario 64 [Player's Choice]", pc_product_id: 5099,
          console_name: 'Nintendo 64', loose_cents: 1100, cib_cents: 1760, new_cents: 3025,
        }],
      }))
    }
    if (u === '/api/products/resolve') return Promise.resolve(jsonResponse(200, product))
    return Promise.resolve(jsonResponse(404, {}))
  })
  vi.stubGlobal('fetch', fetchMock)
  const onPick = vi.fn()
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } })
  render(
    <QueryClientProvider client={qc}>
      <ProxyPicker onPick={onPick} onClose={vi.fn()} />
    </QueryClientProvider>,
  )
  expect(screen.getByRole('radio', { name: 'PriceCharting' })).toBeInTheDocument()
  await userEvent.click(screen.getByRole('radio', { name: 'PriceCharting' }))
  await userEvent.type(screen.getByRole('searchbox', { name: /search/i }), 'mario')
  await userEvent.click(screen.getByRole('button', { name: 'Search' }))
  await userEvent.click(await screen.findByRole('button', { name: /use super mario 64/i }))

  const [resolveUrl, resolveInit] = fetchMock.mock.calls[1] as [string, RequestInit]
  expect(resolveUrl).toBe('/api/products/resolve')
  expect(JSON.parse(resolveInit.body as string)).toEqual({ type: 'pc_listing', pc_product_id: 5099 })
  expect(onPick).toHaveBeenCalledWith(expect.objectContaining({ id: 'p10' }))
})

it('prefills the search box from an initialQuery prop', () => {
  vi.stubGlobal('fetch', vi.fn().mockImplementation((url: string) =>
    String(url).startsWith('/api/search')
      ? Promise.resolve(jsonResponse(200, { degraded: false, results: [] }))
      : Promise.resolve(jsonResponse(404, {})),
  ))
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } })
  render(
    <QueryClientProvider client={qc}>
      <ProxyPicker onPick={vi.fn()} onClose={vi.fn()} initialQuery="Super Mario 64 Player's Choice" />
    </QueryClientProvider>,
  )
  expect(screen.getByRole('searchbox', { name: /search for games, hardware, and pricecharting/i })).toHaveValue(
    "Super Mario 64 Player's Choice",
  )
})
