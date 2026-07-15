import { screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { MemoryRouter } from 'react-router'
import { jsonResponse, putBody } from '../../test/fixtures'
import { renderWithMoney } from '../../test/money'
import type { CatalogPick } from '../catalog/SearchPicker'
import ConfirmStep from './ConfirmStep'
import { defaultDetails } from './DetailsStep'

const pick: CatalogPick = {
  kind: 'game', igdbGameId: 1000, name: 'Chrono Trigger', platformId: 6, platformName: 'SNES',
}

function renderConfirm(onBack = vi.fn(), extra: { manualMatch?: { pcProductId: number; name: string }; onManualMatch?: (m: unknown) => void } = {}) {
  return {
    onBack,
    ...renderWithMoney(
      <MemoryRouter>
        <ConfirmStep
          pick={pick}
          details={defaultDetails()}
          manualMatch={extra.manualMatch}
          onManualMatch={extra.onManualMatch ?? vi.fn()}
          onBack={onBack}
        />
      </MemoryRouter>,
    ),
  }
}

afterEach(() => vi.unstubAllGlobals())

it('reports a resolve failure and keeps the Back action live', async () => {
  vi.stubGlobal('fetch', vi.fn().mockResolvedValue(jsonResponse(500, {})))
  const { onBack } = renderConfirm()
  expect(await screen.findByRole('alert')).toHaveTextContent(/your details are kept/i)
  await userEvent.click(screen.getByRole('button', { name: 'Back' }))
  expect(onBack).toHaveBeenCalled()
})

it('reports a create failure after a successful resolve', async () => {
  const product = {
    id: 'p1', type: 'game', name: 'Chrono Trigger',
    platform: { igdb_platform_id: 6, name: 'SNES' },
    created_at: '2026-07-01T00:00:00Z', updated_at: '2026-07-01T00:00:00Z',
  }
  vi.stubGlobal('fetch', vi.fn().mockImplementation((url: string, init?: RequestInit) => {
    const u = String(url)
    if (u === '/api/products/resolve') return Promise.resolve(jsonResponse(200, product))
    if (u === '/api/entries' && init?.method === 'POST') {
      return Promise.resolve(jsonResponse(500, { code: 'internal', detail: 'creation failed' }))
    }
    return Promise.resolve(jsonResponse(404, {}))
  }))
  renderConfirm()
  expect(await screen.findByText(/confirm: chrono trigger/i)).toBeInTheDocument()
  await userEvent.click(screen.getByRole('button', { name: 'Add to collection' }))
  expect(await screen.findByRole('alert')).toHaveTextContent('creation failed')
})

it('sends the manual match with the resolve', async () => {
  const anchored = {
    id: 'p1', type: 'game', name: 'Chrono Trigger',
    platform: { igdb_platform_id: 6, name: 'SNES' },
    pricecharting: { pc_product_id: 7042, pc_name: 'Chrono Trigger [PAL]', console_name: 'PAL Super Nintendo', match_confidence: 1.0, verified: false, as_of: 'x' },
    created_at: 'x', updated_at: 'x',
  }
  const fetchMock = vi.fn().mockImplementation((url: string) =>
    String(url) === '/api/products/resolve'
      ? Promise.resolve(jsonResponse(200, anchored))
      : Promise.resolve(jsonResponse(404, {})),
  )
  vi.stubGlobal('fetch', fetchMock)
  renderConfirm(vi.fn(), { manualMatch: { pcProductId: 7042, name: 'Chrono Trigger [PAL]' } })
  expect(await screen.findByText(/match 100%/i)).toBeInTheDocument()
  expect(putBody(fetchMock.mock.calls[0][1] as RequestInit)).toEqual({
    type: 'game', igdb_game_id: 1000, platform_igdb_id: 6, pc_product_id: 7042,
  })
})

it('notes when the shared product is already matched differently', async () => {
  const otherAnchor = {
    id: 'p1', type: 'game', name: 'Chrono Trigger',
    platform: { igdb_platform_id: 6, name: 'SNES' },
    pricecharting: { pc_product_id: 5011, pc_name: 'Chrono Trigger', console_name: 'Super Nintendo', match_confidence: 0.93, verified: false, as_of: 'x' },
    created_at: 'x', updated_at: 'x',
  }
  vi.stubGlobal('fetch', vi.fn().mockResolvedValue(jsonResponse(200, otherAnchor)))
  renderConfirm(vi.fn(), { manualMatch: { pcProductId: 7042, name: 'Chrono Trigger [PAL]' } })
  expect(await screen.findByText(/already matched to a different listing/i)).toBeInTheDocument()
  expect(screen.getByText(/needs an admin/i)).toBeInTheDocument()
})

it('offers Match manually on the no-listing card and reports the pick', async () => {
  const unanchored = {
    id: 'p1', type: 'game', name: 'Chrono Trigger',
    platform: { igdb_platform_id: 6, name: 'SNES' },
    created_at: 'x', updated_at: 'x',
  }
  const fetchMock = vi.fn().mockImplementation((url: string) => {
    const u = String(url)
    if (u.startsWith('/api/search')) {
      return Promise.resolve(jsonResponse(200, {
        degraded: false,
        results: [{ type: 'pc_listing', name: 'Chrono Trigger [PAL]', pc_product_id: 7042, console_name: 'PAL Super Nintendo', loose_cents: 9800 }],
      }))
    }
    if (u === '/api/products/resolve') return Promise.resolve(jsonResponse(200, unanchored))
    return Promise.resolve(jsonResponse(404, {}))
  })
  vi.stubGlobal('fetch', fetchMock)
  const onManualMatch = vi.fn()
  renderConfirm(vi.fn(), { onManualMatch })
  expect(await screen.findByText(/no confirmed price listing yet/i)).toBeInTheDocument()
  await userEvent.click(screen.getByRole('button', { name: 'Match manually' }))
  await userEvent.click(await screen.findByRole('button', { name: /use chrono trigger \[pal\]/i }))
  expect(onManualMatch).toHaveBeenCalledWith({ pcProductId: 7042, name: 'Chrono Trigger [PAL]' })
})

it('explains a failed listing resolve with the manual-match copy', async () => {
  vi.stubGlobal('fetch', vi.fn().mockResolvedValue(jsonResponse(404, { code: 'unknown_pc_product' })))
  const { onBack } = renderConfirm(vi.fn(), { manualMatch: { pcProductId: 999999, name: 'Gone' } })
  expect(await screen.findByRole('alert')).toHaveTextContent(/that listing cannot be matched right now/i)
  await userEvent.click(screen.getByRole('button', { name: 'Back' }))
  expect(onBack).toHaveBeenCalled()
})
