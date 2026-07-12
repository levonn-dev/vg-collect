import { screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { MemoryRouter } from 'react-router'
import { jsonResponse } from '../../test/fixtures'
import { renderWithMoney } from '../../test/money'
import type { CatalogPick } from '../catalog/SearchPicker'
import ConfirmStep from './ConfirmStep'
import { defaultDetails } from './DetailsStep'

const pick: CatalogPick = {
  kind: 'game', igdbGameId: 1000, name: 'Chrono Trigger', platformId: 6, platformName: 'SNES',
}

function renderConfirm(onBack = vi.fn()) {
  return {
    onBack,
    ...renderWithMoney(
      <MemoryRouter>
        <ConfirmStep pick={pick} details={defaultDetails()} onBack={onBack} />
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
