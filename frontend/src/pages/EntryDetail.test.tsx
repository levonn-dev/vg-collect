import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { MemoryRouter, Route, Routes } from 'react-router'
import { entryFixture, jsonResponse } from '../test/fixtures'
import { renderWithI18n } from '../test/i18n'
import EntryDetail from './EntryDetail'

function renderDetail(id: string, qc = new QueryClient({ defaultOptions: { queries: { retry: false } } })) {
  return {
    qc,
    ...renderWithI18n(
      <QueryClientProvider client={qc}>
        <MemoryRouter initialEntries={[`/entries/${id}`]}>
          <Routes>
            <Route path="/entries/:id" element={<EntryDetail />} />
            <Route path="/collection" element={<div>collection-page</div>} />
          </Routes>
        </MemoryRouter>
      </QueryClientProvider>,
    ),
  }
}

afterEach(() => vi.unstubAllGlobals())

// Every product-backed fixture here also drives ApprovalNotice's own
// submission fetch; answer it with "no submission" so the banner
// stays hidden and each test can assert its own concern undisturbed.
const noSubmission = () =>
  new Response(
    JSON.stringify({ type: 'about:blank', title: 'x', status: 404, code: 'submission_not_found', detail: 'x' }),
    { status: 404, headers: { 'Content-Type': 'application/problem+json' } },
  )

it('renders the catalog header and the form for a product-backed entry', async () => {
  const e = entryFixture({ display_name: 'Chrono Trigger', value_cents: 4200 })
  vi.stubGlobal('fetch', vi.fn().mockImplementation((url: string) =>
    Promise.resolve(String(url).startsWith('/api/tags')
      ? jsonResponse(200, { tags: [] })
      : String(url).endsWith('/submission')
        ? noSubmission()
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
    if (String(url).endsWith('/submission')) return Promise.resolve(noSubmission())
    if (init?.method === 'PUT') return Promise.resolve(jsonResponse(200, updated))
    return Promise.resolve(jsonResponse(200, e))
  })
  vi.stubGlobal('fetch', fetchMock)
  renderDetail(e.id)
  await userEvent.click(await screen.findByRole('button', { name: /save/i }))
  const put = fetchMock.mock.calls.find((c) => (c[1] as RequestInit | undefined)?.method === 'PUT')
  expect(put?.[0]).toBe(`/api/entries/${e.id}`)
  // Success must be visible, and drifting from the saved state must
  // retract it.
  expect(await screen.findByText('Saved.')).toBeInTheDocument()
  await userEvent.type(screen.getByRole('textbox', { name: 'Notes' }), '!')
  expect(screen.queryByText('Saved.')).not.toBeInTheDocument()
})

it('shows the just-added banner when arriving from the wizard', async () => {
  const e = entryFixture()
  vi.stubGlobal('fetch', vi.fn().mockImplementation((url: string) =>
    Promise.resolve(String(url).startsWith('/api/tags')
      ? jsonResponse(200, { tags: [] })
      : String(url).endsWith('/submission')
        ? noSubmission()
        : jsonResponse(200, e))))
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } })
  renderWithI18n(
    <QueryClientProvider client={qc}>
      <MemoryRouter initialEntries={[{ pathname: `/entries/${e.id}`, state: { justAdded: true } }]}>
        <Routes>
          <Route path="/entries/:id" element={<EntryDetail />} />
        </Routes>
      </MemoryRouter>
    </QueryClientProvider>,
  )
  expect(await screen.findByRole('status')).toHaveTextContent(/added to your collection/i)
})

it('invalidates the dashboard and recommendations caches after a save', async () => {
  const e = entryFixture({ notes: 'old' })
  const updated = { ...e, notes: 'new note' }
  vi.stubGlobal('fetch', vi.fn().mockImplementation((url: string, init?: RequestInit) => {
    if (String(url).startsWith('/api/tags')) return Promise.resolve(jsonResponse(200, { tags: [] }))
    if (String(url).endsWith('/submission')) return Promise.resolve(noSubmission())
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
    if (String(url).endsWith('/submission')) return Promise.resolve(noSubmission())
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
      : String(url).endsWith('/submission')
        ? noSubmission()
        : jsonResponse(200, e))))
  renderDetail(e.id)
  expect(await screen.findByRole('region', { name: 'Pricing' })).toBeInTheDocument()
})

it('deletes after confirmation and navigates home', async () => {
  const e = entryFixture()
  vi.spyOn(window, 'confirm').mockReturnValue(true)
  vi.stubGlobal('fetch', vi.fn().mockImplementation((url: string, init?: RequestInit) => {
    if (String(url).startsWith('/api/tags')) return Promise.resolve(jsonResponse(200, { tags: [] }))
    if (String(url).endsWith('/submission')) return Promise.resolve(noSubmission())
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
    if (String(url).endsWith('/submission')) return Promise.resolve(noSubmission())
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
