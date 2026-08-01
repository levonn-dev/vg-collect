import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { MemoryRouter } from 'react-router'
import { dashboardFixture, entryFixture, jsonResponse, listFixture, meFixture, putBody } from '../test/fixtures'
import { renderWithI18n } from '../test/i18n'
import { defaultListState, toViewParams } from '../lib/listParams'
import Collection from './Collection'

function renderCollection(path = '/') {
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } })
  return renderWithI18n(
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
    if (u.startsWith('/api/me')) return Promise.resolve(jsonResponse(200, meFixture()))
    if (u.startsWith('/api/tags')) return Promise.resolve(jsonResponse(200, { tags: [] }))
    if (u.startsWith('/api/views')) return Promise.resolve(jsonResponse(200, { views: [] }))
    if (u.startsWith('/api/dashboard')) return Promise.resolve(jsonResponse(200, dashboardFixture()))
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

it('shows the query-error banner when the entries list fails to load', async () => {
  vi.stubGlobal('fetch', vi.fn().mockResolvedValue(jsonResponse(500, {})))
  renderCollection()
  expect(await screen.findByRole('alert')).toHaveTextContent(/collection cannot be loaded/i)
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

it('keeps the filter panel closed by default and opens it via the Filters button', async () => {
  stubApi(listFixture([entryFixture()]))
  renderCollection()
  await screen.findByRole('table')
  expect(screen.queryByRole('checkbox', { name: 'Backlog' })).not.toBeInTheDocument()
  await userEvent.click(screen.getByRole('button', { name: 'Filters' }))
  expect(screen.getByRole('checkbox', { name: 'Backlog' })).toBeInTheDocument()
})

it('renders the moved sort/order/group/mode controls via ListControls regardless of panel state', async () => {
  stubApi(listFixture([entryFixture()]))
  renderCollection()
  await screen.findByRole('table')
  expect(screen.getByRole('group', { name: 'Display mode' })).toBeInTheDocument()
  expect(screen.getByLabelText('Sort')).toBeInTheDocument()
  expect(screen.getByRole('button', { name: /^Order:/ })).toBeInTheDocument()
  expect(screen.getByLabelText('Group by')).toBeInTheDocument()
  // The filter panel is still closed - these are not chip checkboxes.
  expect(screen.queryByRole('checkbox', { name: 'Backlog' })).not.toBeInTheDocument()
})

it('drives filters into the URL and the request', async () => {
  stubApi(listFixture([entryFixture()]))
  renderCollection()
  await userEvent.click(await screen.findByRole('button', { name: 'Filters' }))
  await userEvent.click(screen.getByRole('checkbox', { name: 'Backlog' }))
  await screen.findByRole('checkbox', { name: 'Backlog', checked: true })
  const fetchMock = vi.mocked(fetch)
  const urls = fetchMock.mock.calls.map((c) => String(c[0] as string))
  expect(urls.some((u) => u.includes('/api/entries?') && u.includes('status=backlog'))).toBe(true)
})

it('shows the stats strip and re-requests it for the active filters', async () => {
  stubApi(listFixture([entryFixture()]))
  renderCollection('/?status=playing')
  // The collection page carries the dashboard numbers...
  expect(await screen.findByRole('region', { name: 'Totals' })).toBeInTheDocument()
  expect(screen.getByText('$3,842.00')).toBeInTheDocument()
  // ...scoped to the same filters as the list.
  const urls = vi.mocked(fetch).mock.calls.map((c) => String(c[0] as string))
  expect(urls.some((u) => u.startsWith('/api/dashboard?') && u.includes('status=playing'))).toBe(true)
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
  // The filter panel does not auto-open from URL state either - only
  // the Filters count badge signals it (one dimension: status).
  await screen.findByRole('button', { name: 'Filters (1)' })
  expect(screen.queryByRole('checkbox', { name: 'Beaten' })).not.toBeInTheDocument()
  await userEvent.click(screen.getByRole('button', { name: 'Filters (1)' }))
  await screen.findByRole('checkbox', { name: 'Beaten', checked: true })
  const urls = vi.mocked(fetch).mock.calls.map((c) => String(c[0] as string))
  expect(urls.some((u) => u.includes('sort=rating') && u.includes('order=asc'))).toBe(true)
})

it('switches display modes through the URL', async () => {
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

it('hides the Bulk edit toggle entirely while the backlog board is on screen', async () => {
  stubApi(listFixture([entryFixture({ status: 'backlog', backlog_rank: 'b' })]))
  renderCollection('/?status=backlog&sort=backlog_rank')
  await screen.findByRole('region', { name: 'Backlog order' })
  expect(screen.queryByRole('button', { name: 'Bulk edit' })).not.toBeInTheDocument()
})

it('applying a shelf rewrites the URL state', async () => {
  const savedParams = toViewParams({ ...defaultListState(), status: ['backlog'], mode: 'grid' })
  vi.stubGlobal('fetch', vi.fn().mockImplementation((url: string) => {
    const u = String(url)
    if (u.startsWith('/api/views')) {
      return Promise.resolve(jsonResponse(200, { views: [{ id: 'v1', name: 'Wall', params: savedParams, created_at: '2026-07-01T00:00:00Z', updated_at: '2026-07-01T00:00:00Z' }] }))
    }
    if (u.startsWith('/api/tags')) return Promise.resolve(jsonResponse(200, { tags: [] }))
    if (u.startsWith('/api/dashboard')) return Promise.resolve(jsonResponse(200, dashboardFixture()))
    return Promise.resolve(jsonResponse(200, listFixture([entryFixture()])))
  }))
  renderCollection()
  // The select renders (with just "Choose...") before the shelf list loads;
  // wait for the fetched option before choosing it.
  await screen.findByRole('option', { name: 'Wall' })
  await userEvent.selectOptions(screen.getByLabelText('Shelf'), 'v1')
  expect(await screen.findByRole('button', { name: 'Covers', pressed: true })).toBeInTheDocument()
  // Applying a shelf that carries filters does not auto-open the panel -
  // the Filters count badge is the only signal.
  expect(screen.getByRole('button', { name: 'Filters (1)' })).toBeInTheDocument()
  expect(screen.queryByRole('checkbox', { name: 'Backlog' })).not.toBeInTheDocument()
  await userEvent.click(screen.getByRole('button', { name: 'Filters (1)' }))
  expect(screen.getByRole('checkbox', { name: 'Backlog' })).toBeChecked()
})

it('renders the Items/Shelves tablist with Items active by default, showing the list', async () => {
  stubApi(listFixture([entryFixture({ display_name: 'Chrono Trigger' })]))
  renderCollection()
  expect(await screen.findByRole('tablist', { name: 'Collection sections' })).toBeInTheDocument()
  expect(screen.getByRole('tab', { name: 'Items' })).toHaveAttribute('aria-selected', 'true')
  expect(screen.getByRole('tab', { name: 'Shelves' })).toHaveAttribute('aria-selected', 'false')
  expect(await screen.findByRole('link', { name: 'Chrono Trigger' })).toBeInTheDocument()
})

it('switching to Shelves shows the management list and hides the Items content', async () => {
  const view = {
    id: 'v1', name: 'Backlog wall', slug: 'backlog-wall', visibility: 'private' as const,
    params: toViewParams(defaultListState()),
    created_at: '2026-07-01T00:00:00Z', updated_at: '2026-07-01T00:00:00Z',
  }
  vi.stubGlobal('fetch', vi.fn().mockImplementation((url: string) => {
    const u = String(url)
    if (u.startsWith('/api/me')) return Promise.resolve(jsonResponse(200, meFixture()))
    if (u.startsWith('/api/tags')) return Promise.resolve(jsonResponse(200, { tags: [] }))
    if (u.startsWith('/api/views')) return Promise.resolve(jsonResponse(200, { views: [view] }))
    if (u.startsWith('/api/dashboard')) return Promise.resolve(jsonResponse(200, dashboardFixture()))
    return Promise.resolve(jsonResponse(200, listFixture([entryFixture({ display_name: 'Chrono Trigger' })])))
  }))
  renderCollection()
  await screen.findByRole('link', { name: 'Chrono Trigger' })
  await userEvent.click(screen.getByRole('tab', { name: 'Shelves' }))
  expect(await screen.findByText('Backlog wall')).toBeInTheDocument()
  expect(screen.queryByRole('link', { name: 'Chrono Trigger' })).not.toBeInTheDocument()
  expect(screen.queryByRole('table')).not.toBeInTheDocument()
  expect(screen.queryByLabelText('Shelf')).not.toBeInTheDocument()
})

it('switching back to Items restores the URL-driven state', async () => {
  stubApi(listFixture([entryFixture()]))
  renderCollection('/?status=beaten')
  await screen.findByRole('table')
  await screen.findByRole('button', { name: 'Filters (1)' })
  await userEvent.click(screen.getByRole('tab', { name: 'Shelves' }))
  expect(screen.queryByRole('table')).not.toBeInTheDocument()
  await userEvent.click(screen.getByRole('tab', { name: 'Items' }))
  await screen.findByRole('table')
  expect(screen.getByRole('button', { name: 'Filters (1)' })).toBeInTheDocument()
})

it('turning on Bulk edit in table mode shows row checkboxes and the bulk bar', async () => {
  stubApi(listFixture([entryFixture({ display_name: 'Chrono Trigger' })]))
  renderCollection()
  await screen.findByRole('table')
  expect(screen.queryByRole('region', { name: 'Bulk edit' })).not.toBeInTheDocument()
  await userEvent.click(screen.getByRole('button', { name: 'Bulk edit' }))
  expect(screen.getByRole('button', { name: 'Bulk edit' })).toHaveAttribute('aria-pressed', 'true')
  expect(screen.getByRole('checkbox', { name: 'Select Chrono Trigger' })).toBeInTheDocument()
  expect(screen.getByRole('region', { name: 'Bulk edit' })).toBeInTheDocument()
})

it('switching to grid exits bulk mode and drops the selection instead of leaving it to resume', async () => {
  stubApi(listFixture([entryFixture({ display_name: 'Chrono Trigger' })]))
  renderCollection()
  await screen.findByRole('table')
  await userEvent.click(screen.getByRole('button', { name: 'Bulk edit' }))
  await userEvent.click(screen.getByRole('checkbox', { name: 'Select Chrono Trigger' }))

  await userEvent.click(screen.getByRole('button', { name: 'Covers' }))
  expect(screen.queryByRole('button', { name: 'Bulk edit' })).not.toBeInTheDocument()
  expect(screen.queryByRole('region', { name: 'Bulk edit' })).not.toBeInTheDocument()

  await userEvent.click(screen.getByRole('button', { name: 'Table' }))
  await screen.findByRole('table')
  expect(screen.getByRole('button', { name: 'Bulk edit' })).toHaveAttribute('aria-pressed', 'false')
  expect(screen.queryByRole('checkbox', { name: 'Select Chrono Trigger' })).not.toBeInTheDocument()
})

it('applies a bulk change end to end: request body, success announcement, and exiting the bar', async () => {
  const entries = [entryFixture({ display_name: 'Chrono Trigger' }), entryFixture({ display_name: 'Repro Cart' })]
  const fetchMock = vi.fn().mockImplementation((url: string) => {
    const u = String(url)
    if (u === '/api/entries/bulk-update') return Promise.resolve(jsonResponse(200, { updated_count: 2 }))
    if (u.startsWith('/api/me')) return Promise.resolve(jsonResponse(200, meFixture()))
    if (u.startsWith('/api/tags')) return Promise.resolve(jsonResponse(200, { tags: [] }))
    if (u.startsWith('/api/views')) return Promise.resolve(jsonResponse(200, { views: [] }))
    if (u.startsWith('/api/dashboard')) return Promise.resolve(jsonResponse(200, dashboardFixture()))
    return Promise.resolve(jsonResponse(200, listFixture(entries)))
  })
  vi.stubGlobal('fetch', fetchMock)
  renderCollection()
  await screen.findByRole('table')
  await userEvent.click(screen.getByRole('button', { name: 'Bulk edit' }))
  await userEvent.click(screen.getByRole('checkbox', { name: 'Select Chrono Trigger' }))
  await userEvent.click(screen.getByRole('checkbox', { name: 'Select Repro Cart' }))
  await userEvent.selectOptions(screen.getByLabelText('Status'), 'shelved')
  await userEvent.click(screen.getByRole('button', { name: 'Apply' }))

  expect(await screen.findByRole('status')).toHaveTextContent('Updated 2 entries.')
  expect(screen.queryByRole('region', { name: 'Bulk edit' })).not.toBeInTheDocument()
  expect(screen.getByRole('button', { name: 'Bulk edit' })).toHaveAttribute('aria-pressed', 'false')

  const post = fetchMock.mock.calls.find((c) => c[0] === '/api/entries/bulk-update')
  const body = putBody<{ entry_ids: string[]; status: string }>(post?.[1] as RequestInit)
  expect(body.status).toBe('shelved')
  expect(body.entry_ids).toHaveLength(2)
})

it('Cancel in the bulk bar exits bulk mode and clears the selection so re-entering starts fresh', async () => {
  stubApi(listFixture([entryFixture({ display_name: 'Chrono Trigger' })]))
  renderCollection()
  await screen.findByRole('table')
  await userEvent.click(screen.getByRole('button', { name: 'Bulk edit' }))
  await userEvent.click(screen.getByRole('checkbox', { name: 'Select Chrono Trigger' }))
  expect(screen.getByRole('checkbox', { name: 'Select Chrono Trigger' })).toBeChecked()

  await userEvent.click(screen.getByRole('button', { name: 'Cancel' }))
  expect(screen.queryByRole('region', { name: 'Bulk edit' })).not.toBeInTheDocument()
  expect(screen.queryByRole('checkbox', { name: 'Select Chrono Trigger' })).not.toBeInTheDocument()
  expect(screen.getByRole('button', { name: 'Bulk edit' })).toHaveAttribute('aria-pressed', 'false')

  // Re-entering starts fresh: the earlier selection did not survive Cancel.
  await userEvent.click(screen.getByRole('button', { name: 'Bulk edit' }))
  expect(screen.getByRole('checkbox', { name: 'Select Chrono Trigger' })).not.toBeChecked()
  expect(screen.getByRole('checkbox', { name: 'Select all' })).not.toBeChecked()
})
