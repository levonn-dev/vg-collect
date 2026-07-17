import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { render, screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { jsonResponse } from '../../test/fixtures'
import ProductLookup from './ProductLookup'

function renderLookup() {
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } })
  render(
    <QueryClientProvider client={qc}>
      <ProductLookup />
    </QueryClientProvider>,
  )
}

afterEach(() => vi.unstubAllGlobals())

it('fetches a product by id and shows its mapping state with fix actions', async () => {
  const user = userEvent.setup()
  vi.stubGlobal('fetch', vi.fn().mockResolvedValue(jsonResponse(200, {
    id: '4242', type: 'game', name: 'Chrono Trigger',
    platform: { igdb_platform_id: 6, name: 'SNES' },
    pricecharting: {
      pc_product_id: 9010, pc_name: 'Chrono Trigger', console_name: 'Super Nintendo',
      match_confidence: 0.9, verified: false, as_of: '2026-07-01T00:00:00Z',
    },
    created_at: '2026-07-01T00:00:00Z', updated_at: '2026-07-01T00:00:00Z',
  })))
  renderLookup()
  await user.type(screen.getByRole('textbox', { name: 'Product id' }), '4242')
  await user.click(screen.getByRole('button', { name: 'Look up' }))
  expect(await screen.findByText('Chrono Trigger')).toBeInTheDocument()
  expect(screen.getByLabelText('Fix mapping for Chrono Trigger')).toBeInTheDocument()
  expect(screen.getByText(/match 90%/i)).toBeInTheDocument()
})

it('renders a plain message when the id is unknown', async () => {
  const user = userEvent.setup()
  vi.stubGlobal('fetch', vi.fn().mockResolvedValue(jsonResponse(404, {
    type: 'about:blank', title: 'Not Found', status: 404, code: 'product_not_found', detail: 'no such product',
  })))
  renderLookup()
  await user.type(screen.getByRole('textbox', { name: 'Product id' }), 'missing-id')
  await user.click(screen.getByRole('button', { name: 'Look up' }))
  expect(await screen.findByRole('alert')).toHaveTextContent('No product with that id.')
})
