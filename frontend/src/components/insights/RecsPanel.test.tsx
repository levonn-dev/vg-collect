import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { render, screen } from '@testing-library/react'
import { MemoryRouter } from 'react-router'
import { jsonResponse } from '../../test/fixtures'
import RecsPanel from './RecsPanel'

function renderPanel() {
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } })
  return render(
    <QueryClientProvider client={qc}>
      <MemoryRouter>
        <RecsPanel />
      </MemoryRouter>
    </QueryClientProvider>,
  )
}

afterEach(() => vi.unstubAllGlobals())

it('explains an empty answer', async () => {
  vi.stubGlobal('fetch', vi.fn().mockResolvedValue(jsonResponse(200, { degraded: false, recommendations: [] })))
  renderPanel()
  expect(await screen.findByText(/add and rate a few games/i)).toBeInTheDocument()
})

it('reports when recommendations cannot be loaded', async () => {
  vi.stubGlobal('fetch', vi.fn().mockResolvedValue(jsonResponse(500, {})))
  renderPanel()
  expect(await screen.findByText(/recommendations are unavailable right now/i)).toBeInTheDocument()
})
