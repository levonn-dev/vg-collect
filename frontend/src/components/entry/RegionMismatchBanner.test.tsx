import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { jsonResponse, problemResponse } from '../../test/fixtures'
import { renderWithI18n } from '../../test/i18n'
import RegionMismatchBanner from './RegionMismatchBanner'

function renderBanner(overrides: { region?: string; regionMismatchAckAt?: string } = {}) {
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false }, mutations: { retry: false } } })
  const view = renderWithI18n(
    <QueryClientProvider client={qc}>
      <RegionMismatchBanner entryId="e1" productId="p1" region="ntsc_u" {...overrides} />
    </QueryClientProvider>,
  )
  return { ...view, qc }
}

afterEach(() => vi.unstubAllGlobals())

const product = (consoleName: string) =>
  jsonResponse(200, {
    id: 'p1',
    type: 'game',
    name: 'Chrono Trigger',
    pricecharting: {
      pc_product_id: 1, pc_name: 'Chrono Trigger', console_name: consoleName,
      match_confidence: 1, verified: true, as_of: 'x',
    },
    created_at: 'x', updated_at: 'x',
  })

const unmatchedProduct = jsonResponse(200, {
  id: 'p1', type: 'game', name: 'Chrono Trigger', created_at: 'x', updated_at: 'x',
})

it('shows the banner for a mismatched listing and acks on dismiss', async () => {
  const fetchMock = vi
    .fn()
    .mockResolvedValueOnce(product('JP Super Nintendo'))
    .mockResolvedValueOnce(new Response(null, { status: 204 }))
  vi.stubGlobal('fetch', fetchMock)
  renderBanner()
  const dismiss = await screen.findByRole('button', { name: 'Dismiss region mismatch notice' })
  await userEvent.click(dismiss)
  expect(fetchMock.mock.calls[1][0]).toBe('/api/entries/e1/region-mismatch-ack')
  expect(fetchMock.mock.calls[1][1]).toMatchObject({ method: 'POST' })
  expect(screen.queryByRole('status')).not.toBeInTheDocument()
})

it('re-shows the banner when the ack request fails', async () => {
  const fetchMock = vi
    .fn()
    .mockResolvedValueOnce(product('JP Super Nintendo'))
    .mockResolvedValue(problemResponse(500, 'internal', 'x'))
  vi.stubGlobal('fetch', fetchMock)
  renderBanner()
  const dismiss = await screen.findByRole('button', { name: 'Dismiss region mismatch notice' })
  await userEvent.click(dismiss)
  expect(await screen.findByRole('status')).toBeInTheDocument()
  expect(await screen.findByRole('button', { name: 'Dismiss region mismatch notice' })).toBeInTheDocument()
})

it('stays hidden when the matched listing agrees with the entry region', async () => {
  vi.stubGlobal('fetch', vi.fn().mockResolvedValue(product('Super Nintendo')))
  renderBanner()
  await new Promise((r) => setTimeout(r, 0))
  expect(screen.queryByRole('status')).not.toBeInTheDocument()
})

it('stays hidden when already acknowledged', async () => {
  vi.stubGlobal('fetch', vi.fn().mockResolvedValue(product('JP Super Nintendo')))
  renderBanner({ regionMismatchAckAt: '2026-07-19T00:00:00Z' })
  await new Promise((r) => setTimeout(r, 0))
  expect(screen.queryByRole('status')).not.toBeInTheDocument()
})

it('stays hidden while the product carries no price listing yet', async () => {
  vi.stubGlobal('fetch', vi.fn().mockResolvedValue(unmatchedProduct))
  renderBanner()
  await new Promise((r) => setTimeout(r, 0))
  expect(screen.queryByRole('status')).not.toBeInTheDocument()
})
