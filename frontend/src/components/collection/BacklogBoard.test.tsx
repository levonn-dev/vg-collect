import { QueryClient, QueryClientProvider, useQuery } from '@tanstack/react-query'
import { fireEvent, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { MemoryRouter } from 'react-router'
import { fetchEntries } from '../../api/collection'
import { calledPath, entryFixture, jsonResponse, listFixture, problemResponse, putBody } from '../../test/fixtures'
import { renderWithI18n } from '../../test/i18n'
import BacklogBoard from './BacklogBoard'

const entries = [
  entryFixture({ display_name: 'First', backlog_rank: 'am' }),
  entryFixture({ display_name: 'Second', backlog_rank: 'b' }),
  entryFixture({ display_name: 'Third', backlog_rank: 'n' }),
]

// Default page 0, totalCount == entries.length: both edges are the true
// global edges. Page-edge tests pass an explicit page/totalCount instead.
function renderBoard(page = 0, totalCount = entries.length) {
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } })
  return renderWithI18n(
    <QueryClientProvider client={qc}>
      <MemoryRouter>
        <BacklogBoard entries={entries} page={page} totalCount={totalCount} />
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
  expect(calledPath(fetchMock, 0)).toBe(`/api/entries/${entries[0].id}/reorder`)
  expect(await putBody(fetchMock.mock.calls[0][0])).toEqual({
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
  expect(calledPath(fetchMock, 0)).toBe(`/api/entries/${entries[2].id}/reorder`)
  expect(await putBody(fetchMock.mock.calls[0][0])).toEqual({
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

// page 1 of a backlog far bigger than this fixture: neither visible edge is
// the true global edge, so a move there can't resolve to a real neighbor.
it('on a non-edge page, the Up button on the first visible row is disabled', () => {
  vi.stubGlobal('fetch', vi.fn())
  renderBoard(1, 500)
  expect(screen.getByRole('button', { name: 'Move First up' })).toBeDisabled()
})

it('on a non-edge page, a drag to the top slot sends no request', async () => {
  const fetchMock = vi.fn()
  vi.stubGlobal('fetch', fetchMock)
  renderBoard(1, 500)
  stubRowRects()
  // Second dragged onto First's slot computes after_id: null, a page-local
  // top here, not the backlog's true front.
  dragHandle('Drag Second', -50)
  // >50ms: dnd-kit's own detach() removes its document click-swallower on
  // a setTimeout(_, 50) (not just the async onMutate hop a real submit
  // needs); a shorter wait here leaves it armed to eat the next test's click.
  await new Promise((resolve) => setTimeout(resolve, 60))
  expect(fetchMock).not.toHaveBeenCalled()
})

it('on a non-edge page, a mid-page move still sends non-null neighbor ids', async () => {
  const fetchMock = vi.fn().mockResolvedValue(jsonResponse(200, entries[0]))
  vi.stubGlobal('fetch', fetchMock)
  renderBoard(1, 500)
  await userEvent.click(screen.getByRole('button', { name: 'Move First down' }))
  expect(fetchMock).toHaveBeenCalledTimes(1)
  expect(await putBody(fetchMock.mock.calls[0][0])).toEqual({
    after_id: entries[1].id,
    before_id: entries[2].id,
  })
})

it('a 409 conflict reports and recovers', async () => {
  const fetchMock = vi.fn().mockResolvedValue(problemResponse(409, 'conflicting_order', 'neighbors do not straddle'))
  vi.stubGlobal('fetch', fetchMock)
  renderBoard()
  await userEvent.click(screen.getByRole('button', { name: 'Move Second down' }))
  expect(await screen.findByRole('alert')).toHaveTextContent(/changed somewhere else/i)
})

// The optimistic cache reorder only shows up as a re-render when the board
// sits under the same ['entries'] query seam; this harness mirrors it.
function BoardFromCache() {
  const list = useQuery({
    queryKey: ['entries'],
    queryFn: () => fetchEntries(new URLSearchParams()),
  })
  return list.data?.entries
    ? <BacklogBoard entries={list.data.entries} page={0} totalCount={list.data.total_count} />
    : null
}

it('a move reorders the rendered rows and locks the buttons before the server answers', async () => {
  // POST never settles, so an order change can only be the optimistic apply.
  const fetchMock = vi.fn().mockImplementation((url: unknown) =>
    (url as Request).method === 'POST'
      ? new Promise<Response>(() => {})
      : Promise.resolve(jsonResponse(200, listFixture(entries))),
  )
  vi.stubGlobal('fetch', fetchMock)
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } })
  renderWithI18n(
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
  // 'First' sits mid-list after the move, so only the in-flight lock disables it.
  expect(screen.getByRole('button', { name: 'Move First up' })).toBeDisabled()
  expect(screen.getByRole('button', { name: 'Move First down' })).toBeDisabled()
})

// Drag handle carries no disabled attribute (only Move buttons do), so
// proving the guard needs a real drag; stub rects for a deterministic neighbor.
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
  // dnd-kit's PointerSensor binds move/up straight to the activator
  // element (survives it leaving the DOM mid-drag), not document; firing
  // there on document left every drag's listeners stuck live past the
  // test, corrupting whichever test ran next.
  fireEvent.pointerDown(handle, { pointerId: 1, isPrimary: true, button: 0, clientX: 0, clientY: 0 })
  fireEvent.pointerMove(handle, { pointerId: 1, isPrimary: true, clientX: 0, clientY: deltaY })
  fireEvent.pointerUp(handle, { pointerId: 1, isPrimary: true, clientX: 0, clientY: deltaY })
}

it('a drag submitted while a reorder is pending is a no-op', async () => {
  const fetchMock = vi.fn().mockImplementation(() => new Promise<Response>(() => {}))
  vi.stubGlobal('fetch', fetchMock)
  renderBoard()
  stubRowRects()
  dragHandle('Drag First', 100)
  await waitFor(() => expect(fetchMock).toHaveBeenCalledTimes(1))
  dragHandle('Drag First', 100)
  // >50ms: dnd-kit's own detach() removes its document click-swallower on
  // a setTimeout(_, 50) (not just the async onMutate hop the first attempt
  // needed); a shorter wait here leaves it armed to eat the next test's click.
  await new Promise((resolve) => setTimeout(resolve, 60))
  expect(fetchMock).toHaveBeenCalledTimes(1)
})

it('a failed reorder restores the pre-drag order before any refetch resolves', async () => {
  const fetchMock = vi.fn()
    .mockResolvedValueOnce(jsonResponse(200, listFixture(entries)))
    .mockResolvedValueOnce(jsonResponse(500, {}))
    .mockImplementation(() => new Promise<Response>(() => {}))
  vi.stubGlobal('fetch', fetchMock)
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } })
  renderWithI18n(
    <QueryClientProvider client={qc}>
      <MemoryRouter>
        <BoardFromCache />
      </MemoryRouter>
    </QueryClientProvider>,
  )
  const rowNames = () => screen.getAllByRole('link').map((a) => a.textContent)
  await screen.findByRole('button', { name: 'Move Second up' })
  await userEvent.click(screen.getByRole('button', { name: 'Move Second up' }))
  // Third fetch (onSettled refetch) never resolves, so a restored order can
  // only be the onError rollback.
  await waitFor(() => expect(rowNames()).toEqual(['First', 'Second', 'Third']))
  expect(await screen.findByRole('alert')).toHaveTextContent(/could not be saved/i)
})
