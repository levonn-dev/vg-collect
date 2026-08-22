import { screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { calledPath, jsonResponse, putBody, requestPath } from '../../test/fixtures'
import { renderWithMoney } from '../../test/money'
import ProxyPicker from './ProxyPicker'

afterEach(() => vi.unstubAllGlobals())

it('search, platform pick, resolve, and hand back the product', async () => {
  const product = { id: 'p9', type: 'game', name: 'Chrono Trigger', created_at: 'x', updated_at: 'x' }
  const fetchMock = vi.fn().mockImplementation((url: unknown) => {
    const u = requestPath(url)
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
  renderWithMoney(<ProxyPicker onPick={onPick} onClose={vi.fn()} />)
  await userEvent.type(screen.getByRole('searchbox', { name: /search/i }), 'chrono')
  await userEvent.click(screen.getByRole('button', { name: 'Search' }))
  await userEvent.click(await screen.findByRole('button', { name: 'Chrono Trigger on SNES' }))
  expect(onPick).toHaveBeenCalledWith(expect.objectContaining({ id: 'p9' }))
})

it('offers PriceCharting and resolves a pc_listing pick to its product', async () => {
  const product = { id: 'p10', type: 'pc_listing', name: "Super Mario 64 [Player's Choice]", created_at: 'x', updated_at: 'x' }
  const fetchMock = vi.fn().mockImplementation((url: unknown) => {
    const u = requestPath(url)
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
  renderWithMoney(<ProxyPicker onPick={onPick} onClose={vi.fn()} />)
  expect(screen.getByRole('radio', { name: 'PriceCharting' })).toBeInTheDocument()
  await userEvent.click(screen.getByRole('radio', { name: 'PriceCharting' }))
  await userEvent.type(screen.getByRole('searchbox', { name: /search/i }), 'mario')
  await userEvent.click(screen.getByRole('button', { name: 'Search' }))
  await userEvent.click(await screen.findByRole('button', { name: /use super mario 64/i }))

  expect(calledPath(fetchMock, 1)).toBe('/api/products/resolve')
  expect(await putBody(fetchMock.mock.calls[1][0])).toEqual({ type: 'pc_listing', pc_product_id: 5099 })
  expect(onPick).toHaveBeenCalledWith(expect.objectContaining({ id: 'p10' }))
})

it('prefills the search box from an initialQuery prop', () => {
  vi.stubGlobal('fetch', vi.fn().mockImplementation((url: unknown) =>
    requestPath(url).startsWith('/api/search')
      ? Promise.resolve(jsonResponse(200, { degraded: false, results: [] }))
      : Promise.resolve(jsonResponse(404, {})),
  ))
  renderWithMoney(
    <ProxyPicker onPick={vi.fn()} onClose={vi.fn()} initialQuery="Super Mario 64 Player's Choice" />,
  )
  expect(screen.getByRole('searchbox', { name: /search for games, hardware, and pricecharting/i })).toHaveValue(
    "Super Mario 64 Player's Choice",
  )
})

it('reports when the resolve fails', async () => {
  const fetchMock = vi.fn().mockImplementation((url: unknown) => {
    const u = requestPath(url)
    if (u.startsWith('/api/search')) {
      return Promise.resolve(jsonResponse(200, {
        degraded: false,
        results: [{ type: 'game', name: 'Chrono Trigger', igdb_game_id: 1000, platforms: [{ igdb_platform_id: 6, name: 'SNES' }] }],
      }))
    }
    if (u === '/api/products/resolve') return Promise.resolve(jsonResponse(500, {}))
    return Promise.resolve(jsonResponse(404, {}))
  })
  vi.stubGlobal('fetch', fetchMock)
  renderWithMoney(<ProxyPicker onPick={vi.fn()} onClose={vi.fn()} />)
  await userEvent.type(screen.getByRole('searchbox', { name: /search/i }), 'chrono')
  await userEvent.click(screen.getByRole('button', { name: 'Search' }))
  await userEvent.click(await screen.findByRole('button', { name: 'Chrono Trigger on SNES' }))
  expect(await screen.findByRole('alert')).toHaveTextContent(/cannot be used right now/i)
})

it('suppresses the community lane even when the response carries community results', async () => {
  const fetchMock = vi.fn().mockImplementation((url: unknown) => {
    const u = requestPath(url)
    if (u.startsWith('/api/search'))
      return Promise.resolve(jsonResponse(200, {
        degraded: false,
        results: [
          { type: 'game', name: 'Chrono Trigger', igdb_game_id: 1000, platforms: [{ igdb_platform_id: 6, name: 'SNES' }] },
          {
            type: 'game', name: 'Repro Alpha', origin: 'community',
            product_id: 'c0ffee00-0000-4000-8000-000000000001', item_type: 'game', platform_name: 'SNES',
          },
        ],
      }))
    return Promise.resolve(jsonResponse(404, {}))
  })
  vi.stubGlobal('fetch', fetchMock)
  renderWithMoney(<ProxyPicker onPick={vi.fn()} onClose={vi.fn()} initialQuery="repro" />)
  // The search auto-fires; the game result surfaces but the priceless
  // community lane must not - community products are not price sources.
  expect(await screen.findByRole('button', { name: 'Chrono Trigger on SNES' })).toBeInTheDocument()
  expect(screen.queryByText('Community catalog')).not.toBeInTheDocument()
  expect(screen.queryByRole('button', { name: 'Repro Alpha on SNES' })).not.toBeInTheDocument()
})

it('is a modal dialog and starts focus inside it', () => {
  vi.stubGlobal('fetch', vi.fn())
  renderWithMoney(<ProxyPicker onPick={vi.fn()} onClose={vi.fn()} />)
  const dialog = screen.getByRole('dialog')
  expect(dialog).toHaveAttribute('aria-modal', 'true')
  expect(dialog).toContainElement(document.activeElement as HTMLElement)
})

it('returns focus to the opener once the dialog closes', () => {
  vi.stubGlobal('fetch', vi.fn())
  const opener = document.createElement('button')
  document.body.appendChild(opener)
  opener.focus()
  const { unmount } = renderWithMoney(<ProxyPicker onPick={vi.fn()} onClose={vi.fn()} />)
  expect(document.activeElement).not.toBe(opener)
  unmount()
  expect(document.activeElement).toBe(opener)
  opener.remove()
})
