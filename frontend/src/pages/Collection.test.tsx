import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { render, screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { MemoryRouter } from 'react-router'
import { entryFixture, jsonResponse, listFixture } from '../test/fixtures'
import { defaultListState, toViewParams } from '../lib/listParams'
import Collection from './Collection'

function renderCollection(path = '/') {
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } })
  return render(
    <QueryClientProvider client={qc}>
      <MemoryRouter initialEntries={[path]}>
        <Collection />
      </MemoryRouter>
    </QueryClientProvider>,
  )
}

function stubApi(list: unknown) {
  vi.stubGlobal('fetch', vi.fn().mockImplementation((url: string) => {
    const u = String(url)
    if (u.startsWith('/api/tags')) return Promise.resolve(jsonResponse(200, { tags: [] }))
    if (u.startsWith('/api/views')) return Promise.resolve(jsonResponse(200, { views: [] }))
    return Promise.resolve(jsonResponse(200, list))
  }))
}

afterEach(() => vi.unstubAllGlobals())

it('renders table rows with formatted money and placeholders for nulls', async () => {
  const entries = [
    entryFixture({ display_name: 'Chrono Trigger', value_cents: 4200, price_paid_cents: 1500, rating: 9 }),
    entryFixture({ display_name: 'Repro Cart', product_id: undefined, value_cents: undefined }),
  ]
  stubApi(listFixture(entries))
  renderCollection()
  expect(await screen.findByRole('link', { name: 'Chrono Trigger' })).toBeInTheDocument()
  expect(screen.getByText('$42.00')).toBeInTheDocument()
  expect(screen.getByText('$15.00')).toBeInTheDocument()
  const reproRow = screen.getByText('Repro Cart').closest('tr')
  expect(reproRow).toHaveTextContent('-') // null value renders neutrally
})

it('shows the degraded banner when pricing is unavailable', async () => {
  stubApi(listFixture([entryFixture()], { pricing_available: false }))
  renderCollection()
  expect(await screen.findByRole('alert')).toHaveTextContent(/pricing is temporarily unavailable/i)
})

it('shows the empty-collection state with a link to add the first item', async () => {
  stubApi(listFixture([]))
  renderCollection()
  expect(await screen.findByText(/nothing here yet/i)).toBeInTheDocument()
  expect(screen.getByRole('link', { name: 'Add your first item.' })).toHaveAttribute('href', '/add')
})

it('shows the no-matches state when a filter is active', async () => {
  stubApi(listFixture([]))
  renderCollection('/?status=playing')
  expect(await screen.findByText(/nothing matches these filters/i)).toBeInTheDocument()
})

it('pages forward with an offset request', async () => {
  stubApi(listFixture([entryFixture()], { total_count: 450 }))
  renderCollection()
  await userEvent.click(await screen.findByRole('button', { name: 'Next' }))
  const urls = vi.mocked(fetch).mock.calls.map((c) => String(c[0] as string))
  expect(urls.some((u) => u.includes('offset=200'))).toBe(true)
})

it('drives filters into the URL and the request', async () => {
  stubApi(listFixture([entryFixture()]))
  renderCollection()
  await userEvent.click(await screen.findByRole('checkbox', { name: 'Backlog' }))
  await screen.findByRole('checkbox', { name: 'Backlog', checked: true })
  const fetchMock = vi.mocked(fetch)
  const urls = fetchMock.mock.calls.map((c) => String(c[0] as string))
  expect(urls.some((u) => u.includes('/api/entries?') && u.includes('status=backlog'))).toBe(true)
})

it('renders grouped sections in server order', async () => {
  const grouped = {
    pricing_available: true,
    total_count: 3,
    groups: [
      { key: 'snes', label: 'SNES', entries: [entryFixture(), entryFixture()] },
      { key: 'unknown', label: 'Unknown', entries: [entryFixture({ platform: undefined })] },
    ],
  }
  stubApi(grouped)
  renderCollection('/?group_by=platform')
  expect(await screen.findByRole('region', { name: 'SNES' })).toBeInTheDocument()
  const sections = screen.getAllByRole('region').filter((s) => ['SNES', 'Unknown'].includes(s.getAttribute('aria-label') ?? ''))
  expect(sections[0]).toHaveAccessibleName('SNES')
  expect(sections[1]).toHaveAccessibleName('Unknown')
})

it('restores state from the URL on load', async () => {
  stubApi(listFixture([entryFixture()]))
  renderCollection('/?status=beaten&sort=rating&order=asc')
  await screen.findByRole('checkbox', { name: 'Beaten', checked: true })
  const urls = vi.mocked(fetch).mock.calls.map((c) => String(c[0] as string))
  expect(urls.some((u) => u.includes('sort=rating') && u.includes('order=asc'))).toBe(true)
})

it('switches view modes through the URL', async () => {
  stubApi(listFixture([entryFixture({ display_name: 'Chrono Trigger' })]))
  renderCollection('/?mode=grid')
  expect(await screen.findByRole('button', { name: 'Covers', pressed: true })).toBeInTheDocument()
  expect(screen.getByRole('list')).toBeInTheDocument() // the grid ul
  expect(screen.queryByRole('table')).not.toBeInTheDocument()
})

it('switches to the compact list through the mode control', async () => {
  stubApi(listFixture([entryFixture({ display_name: 'Chrono Trigger', value_cents: 4200, status: 'playing', pinned: true })]))
  renderCollection()
  await screen.findByRole('table')
  await userEvent.click(screen.getByRole('button', { name: 'Compact' }))
  expect(await screen.findByRole('button', { name: 'Compact', pressed: true })).toBeInTheDocument()
  expect(screen.queryByRole('table')).not.toBeInTheDocument()
  const row = screen.getByRole('link', { name: 'Chrono Trigger' }).closest('li')
  expect(row).toHaveTextContent('Playing')
  expect(row).toHaveTextContent('$42.00')
  // The static "Pinned" label is now the interactive PinStar (wired via pinSlot).
  expect(screen.getByRole('button', { name: 'Unpin', pressed: true })).toBeInTheDocument()
})

it('renders grouped sections in compact mode', async () => {
  const grouped = {
    pricing_available: true,
    total_count: 2,
    groups: [
      { key: 'snes', label: 'SNES', entries: [entryFixture({ display_name: 'Chrono Trigger' })] },
      { key: 'genesis', label: 'Genesis', entries: [entryFixture({ display_name: 'Sonic' })] },
    ],
  }
  stubApi(grouped)
  renderCollection('/?group_by=platform&mode=compact')
  expect(await screen.findByRole('region', { name: 'SNES' })).toBeInTheDocument()
  expect(screen.getAllByRole('list')).toHaveLength(2) // one compact ul per group
  expect(screen.queryByRole('table')).not.toBeInTheDocument()
})

it('renders the drag board for the backlog-order sort and requests order=asc', async () => {
  stubApi(listFixture([entryFixture({ status: 'backlog', backlog_rank: 'b' })]))
  renderCollection('/?status=backlog&sort=backlog_rank')
  expect(await screen.findByRole('region', { name: 'Backlog order' })).toBeInTheDocument()
  const urls = vi.mocked(fetch).mock.calls.map((c) => String(c[0] as string))
  expect(urls.some((u) => u.includes('sort=backlog_rank') && u.includes('order=asc'))).toBe(true)
})

it('applying a saved view rewrites the URL state', async () => {
  const savedParams = toViewParams({ ...defaultListState(), status: ['backlog'], mode: 'grid' })
  vi.stubGlobal('fetch', vi.fn().mockImplementation((url: string) => {
    const u = String(url)
    if (u.startsWith('/api/views')) {
      return Promise.resolve(jsonResponse(200, { views: [{ id: 'v1', name: 'Wall', params: savedParams, created_at: '2026-07-01T00:00:00Z', updated_at: '2026-07-01T00:00:00Z' }] }))
    }
    if (u.startsWith('/api/tags')) return Promise.resolve(jsonResponse(200, { tags: [] }))
    return Promise.resolve(jsonResponse(200, listFixture([entryFixture()])))
  }))
  renderCollection()
  // The select renders (with just "Choose...") before the view list loads;
  // wait for the fetched option before choosing it.
  await screen.findByRole('option', { name: 'Wall' })
  await userEvent.selectOptions(screen.getByLabelText('Saved view'), 'v1')
  expect(await screen.findByRole('button', { name: 'Covers', pressed: true })).toBeInTheDocument()
  expect(screen.getByRole('checkbox', { name: 'Backlog' })).toBeChecked()
})
