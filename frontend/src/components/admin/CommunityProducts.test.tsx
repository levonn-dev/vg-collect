import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { render, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { jsonResponse } from '../../test/fixtures'
import CommunityProducts from './CommunityProducts'

function renderList() {
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false }, mutations: { retry: false } } })
  render(
    <QueryClientProvider client={qc}>
      <CommunityProducts />
    </QueryClientProvider>,
  )
}

afterEach(() => vi.unstubAllGlobals())

function communityProduct(
  id: string,
  name: string,
  community: { platform_name?: string; first_release_date?: string; cover_url?: string } = {},
) {
  return {
    id, type: 'game', name, origin: 'community',
    community,
    created_at: '2026-07-01T00:00:00Z', updated_at: '2026-07-02T00:00:00Z',
  }
}

it('renders the section heading, explainer, and rows from a stubbed page', async () => {
  vi.stubGlobal('fetch', vi.fn().mockResolvedValue(jsonResponse(200, {
    products: [
      communityProduct('c1', 'Repro Alpha', {
        platform_name: 'SNES', first_release_date: '1994-06-01', cover_url: 'https://img.example/cp.jpg',
      }),
      communityProduct('c2', 'Repro Beta', { platform_name: 'Genesis' }),
    ],
    total_count: 2,
  })))
  renderList()
  expect(await screen.findByRole('region', { name: 'Community products' })).toBeInTheDocument()
  expect(screen.getByText(/delete removes unused ones/i)).toBeInTheDocument()

  expect(screen.getByText('Repro Alpha')).toBeInTheDocument()
  expect(screen.getByText('SNES')).toBeInTheDocument()
  expect(screen.getByText('1994')).toBeInTheDocument()
  // Broken-image-safe like SearchPicker: a cover renders as an <img>...
  expect(document.querySelector('img[src="https://img.example/cp.jpg"]')).toBeInTheDocument()

  // ...and a row with no community cover falls back to the type icon.
  expect(screen.getByText('Repro Beta')).toBeInTheDocument()
  expect(screen.getByText('Genesis')).toBeInTheDocument()
  expect(document.querySelector('svg[data-icon="game"]')).toBeInTheDocument()

  expect(screen.getAllByRole('button', { name: 'Delete' })).toHaveLength(2)
})

it('delete success removes the row via the DELETE call and list invalidation', async () => {
  let listCalls = 0
  const fetchMock = vi.fn().mockImplementation((url: string, init?: RequestInit) => {
    const u = String(url)
    if (u === '/api/admin/products/c1' && init?.method === 'DELETE') {
      return Promise.resolve(new Response(null, { status: 204 }))
    }
    listCalls++
    return Promise.resolve(jsonResponse(200,
      listCalls === 1
        ? { products: [communityProduct('c1', 'Repro Alpha', { platform_name: 'SNES' })], total_count: 1 }
        : { products: [], total_count: 0 },
    ))
  })
  vi.stubGlobal('fetch', fetchMock)
  renderList()
  await userEvent.click(await screen.findByRole('button', { name: 'Delete' }))
  await waitFor(() => expect(screen.queryByText('Repro Alpha')).not.toBeInTheDocument())
  const del = fetchMock.mock.calls.find(
    ([u, i]) => u === '/api/admin/products/c1' && (i as RequestInit | undefined)?.method === 'DELETE',
  )
  expect(del).toBeDefined()
})

it('delete 409 product_referenced shows the inline per-row message and keeps the row', async () => {
  const fetchMock = vi.fn().mockImplementation((url: string, init?: RequestInit) => {
    const u = String(url)
    if (u === '/api/admin/products/c1' && init?.method === 'DELETE') {
      return Promise.resolve(jsonResponse(409, {
        type: 'about:blank', title: 'Conflict', status: 409,
        code: 'product_referenced', detail: '2 entries reference this product',
      }))
    }
    return Promise.resolve(jsonResponse(200, {
      products: [communityProduct('c1', 'Repro Alpha', { platform_name: 'SNES' })], total_count: 1,
    }))
  })
  vi.stubGlobal('fetch', fetchMock)
  renderList()
  await userEvent.click(await screen.findByRole('button', { name: 'Delete' }))
  expect(await screen.findByText('In use by existing entries - cannot delete.')).toBeInTheDocument()
  expect(screen.getByText('Repro Alpha')).toBeInTheDocument()
})
