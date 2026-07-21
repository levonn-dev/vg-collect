import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { render, screen, waitFor, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { jsonResponse } from '../../test/fixtures'
import PromoteCandidates from './PromoteCandidates'

function renderCandidates() {
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false }, mutations: { retry: false } } })
  render(
    <QueryClientProvider client={qc}>
      <PromoteCandidates />
    </QueryClientProvider>,
  )
}

afterEach(() => vi.unstubAllGlobals())

const row = (id: string, name: string) => ({
  product: {
    id, type: 'game', name, origin: 'community',
    community: { platform_name: 'SNES' },
    created_at: '2026-07-01T00:00:00Z', updated_at: '2026-07-02T00:00:00Z',
  },
  candidates: [
    { provider: 'igdb', provider_id: 1011, name: 'Chrono Trigger', score: 0.92, found_at: '2026-07-10T00:00:00Z' },
  ],
})

it('renders the candidate count and a row with its top candidate', async () => {
  vi.stubGlobal('fetch', vi.fn().mockResolvedValue(jsonResponse(200, {
    products: [row('p1', 'Repro Alpha')], total_count: 1,
  })))
  renderCandidates()
  expect(await screen.findByRole('region', { name: 'Promote candidates' })).toBeInTheDocument()
  expect(screen.getByText('1 community product with possible provider matches')).toBeInTheDocument()
  expect(screen.getByText('Repro Alpha')).toBeInTheDocument()
  expect(screen.getByText('Chrono Trigger (0.92)')).toBeInTheDocument()
  // The sweep re-confirms candidates nightly, so the timestamp is the
  // LAST confirmation, not a first discovery; the header must say so.
  expect(screen.getByRole('columnheader', { name: 'Last confirmed' })).toBeInTheDocument()
})

it('pluralizes the count heading (singular at one, plural above)', async () => {
  vi.stubGlobal('fetch', vi.fn().mockResolvedValue(jsonResponse(200, {
    products: [row('p1', 'Repro Alpha'), row('p2', 'Repro Beta')], total_count: 2,
  })))
  renderCandidates()
  expect(await screen.findByText('2 community products with possible provider matches')).toBeInTheDocument()
})

it('expands the promote panel for a row on Review', async () => {
  vi.stubGlobal('fetch', vi.fn().mockResolvedValue(jsonResponse(200, {
    products: [row('p1', 'Repro Alpha')], total_count: 1,
  })))
  renderCandidates()
  await userEvent.click(await screen.findByRole('button', { name: 'Review' }))
  expect(screen.getByLabelText('Promote Repro Alpha')).toBeInTheDocument()
})

const twoCandidateRow = (id: string, name: string) => ({
  product: {
    id, type: 'game', name, origin: 'community',
    community: { platform_name: 'SNES' },
    created_at: '2026-07-01T00:00:00Z', updated_at: '2026-07-02T00:00:00Z',
  },
  candidates: [
    { provider: 'igdb', provider_id: 1011, name: 'Chrono Trigger', score: 0.92, found_at: '2026-07-10T00:00:00Z' },
    { provider: 'igdb', provider_id: 2022, name: 'Secret of Mana', score: 0.80, found_at: '2026-07-10T00:00:00Z' },
  ],
})

it('dismiss inside an open panel refreshes it in place from the worklist refetch', async () => {
  const initial = twoCandidateRow('p1', 'Repro Alpha')
  const afterDismiss = { product: initial.product, candidates: [initial.candidates[1]] }
  const fetchMock = vi
    .fn()
    .mockResolvedValueOnce(jsonResponse(200, { products: [initial], total_count: 1 }))
    .mockResolvedValueOnce(new Response(null, { status: 204 }))
    .mockResolvedValueOnce(jsonResponse(200, { products: [afterDismiss], total_count: 1 }))
  vi.stubGlobal('fetch', fetchMock)
  renderCandidates()
  await userEvent.click(await screen.findByRole('button', { name: 'Review' }))
  const panel = () => screen.getByLabelText('Promote Repro Alpha')
  expect(within(panel()).getAllByRole('button', { name: 'Dismiss' })).toHaveLength(2)
  await userEvent.click(within(panel()).getAllByRole('button', { name: 'Dismiss' })[0])
  // Dismiss invalidates admin queries; the worklist refetches its page and
  // the still-open panel - which derives its candidates from rows.find,
  // not a snapshot taken when it opened - picks up the reduced list.
  await waitFor(() => expect(within(panel()).getAllByRole('button', { name: 'Dismiss' })).toHaveLength(1))
  expect(within(panel()).getByText('Secret of Mana', { exact: false })).toBeInTheDocument()
  expect(within(panel()).queryByText('Chrono Trigger', { exact: false })).not.toBeInTheDocument()
})
