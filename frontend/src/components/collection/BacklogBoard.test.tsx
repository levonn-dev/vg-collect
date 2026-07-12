import { QueryClient, QueryClientProvider, useQuery } from '@tanstack/react-query'
import { fireEvent, render, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { MemoryRouter } from 'react-router'
import { fetchEntries } from '../../api/collection'
import { entryFixture, jsonResponse, listFixture, putBody } from '../../test/fixtures'
import BacklogBoard from './BacklogBoard'

const entries = [
  entryFixture({ display_name: 'First', backlog_rank: 'am' }),
  entryFixture({ display_name: 'Second', backlog_rank: 'b' }),
  entryFixture({ display_name: 'Third', backlog_rank: 'n' }),
]

function renderBoard() {
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } })
  return render(
    <QueryClientProvider client={qc}>
      <MemoryRouter>
        <BacklogBoard entries={entries} />
      </MemoryRouter>
    </QueryClientProvider>,
  )
}

afterEach(() => vi.unstubAllGlobals())

it('renders the given order with drag handles and move buttons', () => {
  vi.stubGlobal('fetch', vi.fn())
  renderBoard()
  const items = screen.getAllByRole('listitem')
  expect(items[0]).toHaveTextContent('First')
  expect(items[2]).toHaveTextContent('Third')
  expect(screen.getAllByRole('button', { name: /drag/i })).toHaveLength(3)
})

it('Move down posts the visual-neighbor pair', async () => {
  const fetchMock = vi.fn().mockResolvedValue(jsonResponse(200, entries[0]))
  vi.stubGlobal('fetch', fetchMock)
  renderBoard()
  await userEvent.click(screen.getByRole('button', { name: 'Move First down' }))
  expect(fetchMock).toHaveBeenCalledTimes(1)
  const [url, init] = fetchMock.mock.calls[0] as [string, RequestInit]
  expect(url).toBe(`/api/entries/${entries[0].id}/reorder`)
  expect(putBody(init)).toEqual({
    after_id: entries[1].id,
    before_id: entries[2].id,
  })
})

it('Move up posts the visual-neighbor pair', async () => {
  const fetchMock = vi.fn().mockResolvedValue(jsonResponse(200, entries[2]))
  vi.stubGlobal('fetch', fetchMock)
  renderBoard()
  await userEvent.click(screen.getByRole('button', { name: 'Move Third up' }))
  expect(fetchMock).toHaveBeenCalledTimes(1)
  const [url, init] = fetchMock.mock.calls[0] as [string, RequestInit]
  expect(url).toBe(`/api/entries/${entries[2].id}/reorder`)
  expect(putBody(init)).toEqual({
    after_id: entries[0].id,
    before_id: entries[1].id,
  })
})

it('edge moves are disabled', () => {
  vi.stubGlobal('fetch', vi.fn())
  renderBoard()
  expect(screen.getByRole('button', { name: 'Move First up' })).toBeDisabled()
  expect(screen.getByRole('button', { name: 'Move Third down' })).toBeDisabled()
})

it('a 409 conflict reports and recovers', async () => {
  const fetchMock = vi.fn().mockResolvedValue(jsonResponse(409, {
    type: 'about:blank', title: 'Conflict', status: 409,
    code: 'conflicting_order', detail: 'neighbors do not straddle',
  }))
  vi.stubGlobal('fetch', fetchMock)
  renderBoard()
  await userEvent.click(screen.getByRole('button', { name: 'Move Second down' }))
  expect(await screen.findByRole('alert')).toHaveTextContent(/changed somewhere else/i)
})

// The page feeds BacklogBoard from the ['entries'] query, so the
// optimistic cache reorder only shows up as a re-render when the
// board sits under that same seam; this harness mirrors it.
function BoardFromCache() {
  const list = useQuery({
    queryKey: ['entries'],
    queryFn: () => fetchEntries(new URLSearchParams()),
  })
  return list.data?.entries ? <BacklogBoard entries={list.data.entries} /> : null
}

it('a move reorders the rendered rows and locks the buttons before the server answers', async () => {
  // The reorder POST never settles, so an order change can only be the
  // optimistic cache apply, never an invalidation refetch.
  const fetchMock = vi.fn().mockImplementation((_url: string, init?: RequestInit) =>
    init?.method === 'POST'
      ? new Promise<Response>(() => {})
      : Promise.resolve(jsonResponse(200, listFixture(entries))),
  )
  vi.stubGlobal('fetch', fetchMock)
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } })
  render(
    <QueryClientProvider client={qc}>
      <MemoryRouter>
        <BoardFromCache />
      </MemoryRouter>
    </QueryClientProvider>,
  )
  const rowNames = () => screen.getAllByRole('link').map((a) => a.textContent)
  await screen.findByRole('button', { name: 'Move Second up' })
  expect(rowNames()).toEqual(['First', 'Second', 'Third'])
  await userEvent.click(screen.getByRole('button', { name: 'Move Second up' }))
  expect(rowNames()).toEqual(['Second', 'First', 'Third'])
  // 'First' sits mid-list after the optimistic move, so neither edge
  // flag applies - only the in-flight lock can disable its buttons.
  expect(screen.getByRole('button', { name: 'Move First up' })).toBeDisabled()
  expect(screen.getByRole('button', { name: 'Move First down' })).toBeDisabled()
})

// The drag handle carries no `disabled` attribute while a reorder is
// pending (only the Move buttons do), so proving the guard needs an
// actual drag: stub every row's rect so closestCenter resolves to a
// distinct, deterministic neighbor instead of jsdom's zero-sized default.
function stubRowRects() {
  screen.getAllByRole('listitem').forEach((li, i) => {
    const top = i * 50
    li.getBoundingClientRect = () => ({
      x: 0, y: top, width: 300, height: 40, top, left: 0, right: 300, bottom: top + 40,
      toJSON: () => ({}),
    })
  })
}

function dragHandle(name: string, deltaY: number) {
  const handle = screen.getByRole('button', { name })
  fireEvent.pointerDown(handle, { pointerId: 1, isPrimary: true, button: 0, clientX: 0, clientY: 0 })
  fireEvent.pointerMove(document, { pointerId: 1, isPrimary: true, clientX: 0, clientY: deltaY })
  fireEvent.pointerUp(document, { pointerId: 1, isPrimary: true, clientX: 0, clientY: deltaY })
}

it('a drag submitted while a reorder is pending is a no-op', async () => {
  const fetchMock = vi.fn().mockImplementation(() => new Promise<Response>(() => {}))
  vi.stubGlobal('fetch', fetchMock)
  renderBoard()
  stubRowRects()
  dragHandle('Drag First', 100)
  await waitFor(() => expect(fetchMock).toHaveBeenCalledTimes(1))
  dragHandle('Drag First', 100)
  // Give the guarded second attempt the same async onMutate hop the
  // first one needed, so a regression would have time to show up.
  await new Promise((resolve) => setTimeout(resolve, 20))
  expect(fetchMock).toHaveBeenCalledTimes(1)
})

it('a failed reorder restores the pre-drag order before any refetch resolves', async () => {
  const fetchMock = vi.fn()
    .mockResolvedValueOnce(jsonResponse(200, listFixture(entries)))
    .mockResolvedValueOnce(jsonResponse(500, {}))
    .mockImplementation(() => new Promise<Response>(() => {}))
  vi.stubGlobal('fetch', fetchMock)
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } })
  render(
    <QueryClientProvider client={qc}>
      <MemoryRouter>
        <BoardFromCache />
      </MemoryRouter>
    </QueryClientProvider>,
  )
  const rowNames = () => screen.getAllByRole('link').map((a) => a.textContent)
  await screen.findByRole('button', { name: 'Move Second up' })
  await userEvent.click(screen.getByRole('button', { name: 'Move Second up' }))
  // The third fetch call (the onSettled refetch) never resolves, so a
  // restored order can only be the onError rollback.
  await waitFor(() => expect(rowNames()).toEqual(['First', 'Second', 'Third']))
  expect(await screen.findByRole('alert')).toHaveTextContent(/could not be saved/i)
})
