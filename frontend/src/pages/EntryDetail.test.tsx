import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { render, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { MemoryRouter, Route, Routes } from 'react-router'
import { entryFixture, jsonResponse } from '../test/fixtures'
import EntryDetail from './EntryDetail'

function renderDetail(id: string, qc = new QueryClient({ defaultOptions: { queries: { retry: false } } })) {
  return {
    qc,
    ...render(
      <QueryClientProvider client={qc}>
        <MemoryRouter initialEntries={[`/entries/${id}`]}>
          <Routes>
            <Route path="/entries/:id" element={<EntryDetail />} />
            <Route path="/" element={<div>collection-page</div>} />
          </Routes>
        </MemoryRouter>
      </QueryClientProvider>,
    ),
  }
}

afterEach(() => vi.unstubAllGlobals())

it('renders the catalog header and the form for a product-backed entry', async () => {
  const e = entryFixture({ display_name: 'Chrono Trigger', value_cents: 4200 })
  vi.stubGlobal('fetch', vi.fn().mockImplementation((url: string) =>
    Promise.resolve(String(url).startsWith('/api/tags')
      ? jsonResponse(200, { tags: [] })
      : jsonResponse(200, e))))
  renderDetail(e.id)
  expect(await screen.findByRole('heading', { name: 'Chrono Trigger' })).toBeInTheDocument()
  expect(screen.getByText(/SNES/)).toBeInTheDocument()
  expect(screen.getByRole('form', { name: 'Entry editor' })).toBeInTheDocument()
})

it('saves through the form and shows the refreshed entry', async () => {
  const e = entryFixture({ notes: 'old' })
  const updated = { ...e, notes: 'new note' }
  const fetchMock = vi.fn().mockImplementation((url: string, init?: RequestInit) => {
    if (String(url).startsWith('/api/tags')) return Promise.resolve(jsonResponse(200, { tags: [] }))
    if (init?.method === 'PUT') return Promise.resolve(jsonResponse(200, updated))
    return Promise.resolve(jsonResponse(200, e))
  })
  vi.stubGlobal('fetch', fetchMock)
  renderDetail(e.id)
  await userEvent.click(await screen.findByRole('button', { name: /save/i }))
  const put = fetchMock.mock.calls.find((c) => (c[1] as RequestInit | undefined)?.method === 'PUT')
  expect(put?.[0]).toBe(`/api/entries/${e.id}`)
})

it('invalidates the dashboard and recommendations caches after a save', async () => {
  const e = entryFixture({ notes: 'old' })
  const updated = { ...e, notes: 'new note' }
  vi.stubGlobal('fetch', vi.fn().mockImplementation((url: string, init?: RequestInit) => {
    if (String(url).startsWith('/api/tags')) return Promise.resolve(jsonResponse(200, { tags: [] }))
    if (init?.method === 'PUT') return Promise.resolve(jsonResponse(200, updated))
    return Promise.resolve(jsonResponse(200, e))
  }))
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } })
  qc.setQueryData(['dashboard'], { stale: true })
  qc.setQueryData(['recommendations'], { stale: true })
  renderDetail(e.id, qc)
  await userEvent.click(await screen.findByRole('button', { name: /save/i }))
  await waitFor(() => expect(qc.getQueryState(['recommendations'])?.isInvalidated).toBe(true))
  expect(qc.getQueryState(['dashboard'])?.isInvalidated).toBe(true)
})

it('surfaces a 404 pricing problem from the save', async () => {
  const e = entryFixture()
  vi.stubGlobal('fetch', vi.fn().mockImplementation((url: string, init?: RequestInit) => {
    if (String(url).startsWith('/api/tags')) return Promise.resolve(jsonResponse(200, { tags: [] }))
    if (init?.method === 'PUT') {
      return Promise.resolve(jsonResponse(404, {
        type: 'about:blank', title: 'Not Found', status: 404,
        code: 'unknown_pricing_product', detail: 'no such pricing product in the catalog',
      }))
    }
    return Promise.resolve(jsonResponse(200, e))
  }))
  renderDetail(e.id)
  await userEvent.click(await screen.findByRole('button', { name: /save/i }))
  expect(await screen.findByRole('alert')).toHaveTextContent(/no such pricing product/i)
})

it('renders the pricing panel', async () => {
  const e = entryFixture()
  vi.stubGlobal('fetch', vi.fn().mockImplementation((url: string) =>
    Promise.resolve(String(url).startsWith('/api/tags')
      ? jsonResponse(200, { tags: [] })
      : jsonResponse(200, e))))
  renderDetail(e.id)
  expect(await screen.findByRole('region', { name: 'Pricing' })).toBeInTheDocument()
})

it('deletes after confirmation and navigates home', async () => {
  const e = entryFixture()
  vi.spyOn(window, 'confirm').mockReturnValue(true)
  vi.stubGlobal('fetch', vi.fn().mockImplementation((url: string, init?: RequestInit) => {
    if (String(url).startsWith('/api/tags')) return Promise.resolve(jsonResponse(200, { tags: [] }))
    if (init?.method === 'DELETE') return Promise.resolve(new Response(null, { status: 204 }))
    return Promise.resolve(jsonResponse(200, e))
  }))
  renderDetail(e.id)
  await userEvent.click(await screen.findByRole('button', { name: /delete/i }))
  expect(await screen.findByText('collection-page')).toBeInTheDocument()
})

it('invalidates dashboard/recommendations and drops the entry cache on delete', async () => {
  const e = entryFixture()
  vi.spyOn(window, 'confirm').mockReturnValue(true)
  vi.stubGlobal('fetch', vi.fn().mockImplementation((url: string, init?: RequestInit) => {
    if (String(url).startsWith('/api/tags')) return Promise.resolve(jsonResponse(200, { tags: [] }))
    if (init?.method === 'DELETE') return Promise.resolve(new Response(null, { status: 204 }))
    return Promise.resolve(jsonResponse(200, e))
  }))
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } })
  qc.setQueryData(['dashboard'], { stale: true })
  qc.setQueryData(['recommendations'], { stale: true })
  renderDetail(e.id, qc)
  await userEvent.click(await screen.findByRole('button', { name: /delete/i }))
  expect(await screen.findByText('collection-page')).toBeInTheDocument()

  expect(qc.getQueryState(['dashboard'])?.isInvalidated).toBe(true)
  expect(qc.getQueryState(['recommendations'])?.isInvalidated).toBe(true)
  // The back button must not be able to render the deleted entry from cache.
  expect(qc.getQueryData(['entry', e.id])).toBeUndefined()
})
