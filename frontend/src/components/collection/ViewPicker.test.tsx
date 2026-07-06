import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { render, screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { jsonResponse } from '../../test/fixtures'
import { defaultListState, toViewParams } from '../../lib/listParams'
import ViewPicker from './ViewPicker'

const savedState = { ...defaultListState(), status: ['backlog' as const], mode: 'grid' as const }
const view = {
  id: 'v1', name: 'Backlog wall', params: toViewParams(savedState),
  created_at: '2026-07-01T00:00:00Z', updated_at: '2026-07-01T00:00:00Z',
}

function renderPicker(state = defaultListState(), onApply = vi.fn()) {
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } })
  render(
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

it('applies a saved view: decoded state plus the view id', async () => {
  vi.stubGlobal('fetch', vi.fn().mockResolvedValue(jsonResponse(200, { views: [view] })))
  const onApply = renderPicker()
  // The select is present from the first render (its label is static);
  // wait for the fetched option itself before choosing it, otherwise
  // selectOptions can run before the view list has loaded.
  await screen.findByRole('option', { name: view.name })
  await userEvent.selectOptions(screen.getByLabelText('Saved view'), 'v1')
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
  await userEvent.click(await screen.findByRole('button', { name: /save view/i }))
  const post = fetchMock.mock.calls[1]
  expect(post[0]).toBe('/api/views')
  const body = JSON.parse((post[1] as RequestInit).body as string) as { name: string; params: unknown }
  expect(body.name).toBe('New view')
  expect(body.params).toEqual(toViewParams(savedState))
  expect(onApply).toHaveBeenCalledWith(expect.objectContaining({ viewId: 'v2' }))
})

it('updates the active view with the current state', async () => {
  const fetchMock = vi.fn().mockImplementation((_url: string, init?: RequestInit) =>
    Promise.resolve(init?.method === 'PUT'
      ? jsonResponse(200, view)
      : jsonResponse(200, { views: [view] })))
  vi.stubGlobal('fetch', fetchMock)
  const current = { ...savedState, viewId: 'v1', packaging: ['cib' as const] }
  renderPicker(current)
  await userEvent.click(await screen.findByRole('button', { name: /update view/i }))
  const put = fetchMock.mock.calls.find((c) => (c[1] as RequestInit | undefined)?.method === 'PUT')
  expect(put?.[0]).toBe('/api/views/v1')
  const body = JSON.parse((put?.[1] as RequestInit).body as string) as { params: Record<string, unknown> }
  expect(body.params.packaging).toEqual(['cib'])
})

it('deletes the active view and resets the applied id', async () => {
  const fetchMock = vi.fn().mockImplementation((_url: string, init?: RequestInit) =>
    Promise.resolve(init?.method === 'DELETE'
      ? new Response(null, { status: 204 })
      : jsonResponse(200, { views: [view] })))
  vi.stubGlobal('fetch', fetchMock)
  vi.spyOn(window, 'confirm').mockReturnValue(true)
  const onApply = renderPicker({ ...savedState, viewId: 'v1' })
  await userEvent.click(await screen.findByRole('button', { name: /delete view/i }))
  expect(fetchMock.mock.calls.some((c) => (c[1] as RequestInit | undefined)?.method === 'DELETE')).toBe(true)
  expect(onApply).toHaveBeenCalledWith(expect.objectContaining({ viewId: undefined }))
})

it('surfaces a name conflict on save', async () => {
  const fetchMock = vi.fn()
    .mockResolvedValueOnce(jsonResponse(200, { views: [view] }))
    .mockResolvedValueOnce(jsonResponse(409, {
      type: 'about:blank', title: 'Conflict', status: 409, code: 'name_taken', detail: 'view name already in use',
    }))
  vi.stubGlobal('fetch', fetchMock)
  vi.spyOn(window, 'prompt').mockReturnValue('Backlog wall')
  renderPicker(savedState)
  await userEvent.click(await screen.findByRole('button', { name: /save view/i }))
  expect(await screen.findByRole('alert')).toHaveTextContent(/already in use/i)
})
