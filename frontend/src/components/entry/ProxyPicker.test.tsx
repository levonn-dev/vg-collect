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
