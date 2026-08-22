import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { jsonResponse, problemResponse, putBody, requestPath } from '../../test/fixtures'
import { renderWithI18n } from '../../test/i18n'
import { defaultListState, toViewParams } from '../../lib/listParams'
import ViewPicker from './ViewPicker'

const savedState = { ...defaultListState(), status: ['backlog' as const], mode: 'grid' as const }
const view = {
  id: 'v1', name: 'Backlog wall', slug: 'backlog-wall', visibility: 'private' as const,
  params: toViewParams(savedState),
  created_at: '2026-07-01T00:00:00Z', updated_at: '2026-07-01T00:00:00Z',
}

function renderPicker(state = defaultListState(), onApply = vi.fn()) {
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false, staleTime: Infinity } } })
  renderWithI18n(
    <QueryClientProvider client={qc}>
      <ViewPicker state={state} onApply={onApply} />
    </QueryClientProvider>,
  )
  return onApply
}

afterEach(() => {
  vi.unstubAllGlobals()
  vi.restoreAllMocks()
})

it('applies a shelf: decoded state plus the view id', async () => {
  vi.stubGlobal('fetch', vi.fn().mockResolvedValue(jsonResponse(200, { views: [view] })))
  const onApply = renderPicker()
  // The select is present from the first render (its label is static);
  // wait for the fetched option itself before choosing it, otherwise
  // selectOptions can run before the shelf list has loaded.
  await screen.findByRole('option', { name: view.name })
  await userEvent.selectOptions(screen.getByLabelText('Shelf'), 'v1')
  expect(onApply).toHaveBeenCalledWith({ ...savedState, viewId: 'v1' })
})

it('saves the current state under a prompted name', async () => {
  const fetchMock = vi.fn()
    .mockResolvedValueOnce(jsonResponse(200, { views: [] }))
    .mockResolvedValueOnce(jsonResponse(201, { ...view, id: 'v2', name: 'New view' }))
    .mockResolvedValueOnce(jsonResponse(200, { views: [{ ...view, id: 'v2', name: 'New view' }] }))
  vi.stubGlobal('fetch', fetchMock)
  vi.spyOn(window, 'prompt').mockReturnValue('New view')
  const onApply = renderPicker(savedState)
  await userEvent.click(await screen.findByRole('button', { name: /save shelf/i }))
  const post = fetchMock.mock.calls[1]
  expect(requestPath(post[0])).toBe('/api/views')
  const body = await putBody<{ name: string; params: unknown }>(post[0])
  expect(body.name).toBe('New view')
  expect(body.params).toEqual(toViewParams(savedState))
  expect(onApply).toHaveBeenCalledWith(expect.objectContaining({ viewId: 'v2' }))
})

it('updates the active shelf with the current state', async () => {
  const fetchMock = vi.fn().mockImplementation((url: unknown) =>
    Promise.resolve((url as Request).method === 'PUT'
      ? jsonResponse(200, view)
      : jsonResponse(200, { views: [view] })))
  vi.stubGlobal('fetch', fetchMock)
  const current = { ...savedState, viewId: 'v1', packaging: ['cib' as const] }
  renderPicker(current)
  await userEvent.click(await screen.findByRole('button', { name: /update shelf/i }))
  const put = fetchMock.mock.calls.find((c) => (c[0] as Request).method === 'PUT')
  expect(requestPath(put?.[0])).toBe('/api/views/v1')
  const body = await putBody<{ params: Record<string, unknown> }>(put?.[0])
  expect(body.params.packaging).toEqual(['cib'])
})

it('deletes the active shelf and resets the applied id', async () => {
  const fetchMock = vi.fn().mockImplementation((url: unknown) =>
    Promise.resolve((url as Request).method === 'DELETE'
      ? new Response(null, { status: 204 })
      : jsonResponse(200, { views: [view] })))
  vi.stubGlobal('fetch', fetchMock)
  vi.spyOn(window, 'confirm').mockReturnValue(true)
  const onApply = renderPicker({ ...savedState, viewId: 'v1' })
  await userEvent.click(await screen.findByRole('button', { name: /delete shelf/i }))
  expect(fetchMock.mock.calls.some((c) => (c[0] as Request).method === 'DELETE')).toBe(true)
  expect(onApply).toHaveBeenCalledWith(expect.objectContaining({ viewId: undefined }))
})

it('surfaces a name conflict on save', async () => {
  const fetchMock = vi.fn()
    .mockResolvedValueOnce(jsonResponse(200, { views: [view] }))
    .mockResolvedValueOnce(problemResponse(409, 'name_taken', 'view name already in use'))
  vi.stubGlobal('fetch', fetchMock)
  vi.spyOn(window, 'prompt').mockReturnValue('Backlog wall')
  renderPicker(savedState)
  await userEvent.click(await screen.findByRole('button', { name: /save shelf/i }))
  expect(await screen.findByRole('alert')).toHaveTextContent(/already in use/i)
})

it('clears a stale save error once a later, different action succeeds', async () => {
  const fetchMock = vi.fn().mockImplementation((url: unknown) => {
    if ((url as Request).method === 'POST') {
      return Promise.resolve(problemResponse(409, 'name_taken', 'view name already in use'))
    }
    if ((url as Request).method === 'PUT') return Promise.resolve(jsonResponse(200, view))
    return Promise.resolve(jsonResponse(200, { views: [view] }))
  })
  vi.stubGlobal('fetch', fetchMock)
  vi.spyOn(window, 'prompt').mockReturnValue('Backlog wall')
  renderPicker({ ...savedState, viewId: 'v1' })
  await userEvent.click(await screen.findByRole('button', { name: /save shelf/i }))
  expect(await screen.findByRole('alert')).toHaveTextContent(/already in use/i)
  // A later, unrelated mutation (update, not save) succeeding must not
  // leave the earlier save failure on screen.
  await userEvent.click(screen.getByRole('button', { name: /update shelf/i }))
  await waitFor(() => expect(screen.queryByRole('alert')).not.toBeInTheDocument())
})

it('the plain Update shelf button round-trips the active shelf\'s own visibility instead of resetting it', async () => {
  const listed = { ...view, visibility: 'listed' as const }
  const fetchMock = vi.fn().mockImplementation((url: unknown) =>
    Promise.resolve((url as Request).method === 'PUT'
      ? jsonResponse(200, listed)
      : jsonResponse(200, { views: [listed] })))
  vi.stubGlobal('fetch', fetchMock)
  renderPicker({ ...savedState, viewId: listed.id })
  await userEvent.click(await screen.findByRole('button', { name: /update shelf/i }))
  const put = fetchMock.mock.calls.find((c) => (c[0] as Request).method === 'PUT')
  expect((await putBody<{ visibility: string }>(put?.[0])).visibility).toBe('listed')
})
