import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { screen, waitFor, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import type { BulkUpdateRequest, Tag } from '../../api/collection'
import { jsonResponse, problemResponse, putBody } from '../../test/fixtures'
import { renderWithI18n } from '../../test/i18n'
import BulkEditBar from './BulkEditBar'

const tags: Tag[] = [
  { id: 't1', name: 'rpg', entry_count: 3 },
  { id: 't2', name: 'snes', entry_count: 1 },
]

function renderBar(opts: { selected?: Set<string>; tags?: Tag[] } = {}) {
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } })
  const invalidateSpy = vi.spyOn(qc, 'invalidateQueries')
  const onCancel = vi.fn()
  const onApplied = vi.fn()
  renderWithI18n(
    <QueryClientProvider client={qc}>
      <BulkEditBar
        selected={opts.selected ?? new Set(['e1', 'e2'])}
        tags={opts.tags ?? tags}
        onCancel={onCancel}
        onApplied={onApplied}
      />
    </QueryClientProvider>,
  )
  return { invalidateSpy, onCancel, onApplied }
}

afterEach(() => vi.unstubAllGlobals())

it('shows the selection count', () => {
  renderBar({ selected: new Set(['e1', 'e2', 'e3']) })
  expect(screen.getByText('3 selected')).toBeInTheDocument()
})

it('disables Apply until at least one action is chosen', async () => {
  renderBar()
  expect(screen.getByRole('button', { name: 'Apply' })).toBeDisabled()
  await userEvent.selectOptions(screen.getByLabelText('Status'), 'shelved')
  expect(screen.getByRole('button', { name: 'Apply' })).toBeEnabled()
})

it('disables Apply when nothing is selected, even with an action chosen', async () => {
  renderBar({ selected: new Set() })
  await userEvent.selectOptions(screen.getByLabelText('Status'), 'shelved')
  expect(screen.getByRole('button', { name: 'Apply' })).toBeDisabled()
})

it('disables Apply and shows the cap message once the selection exceeds 200', async () => {
  const selected = new Set(Array.from({ length: 201 }, (_, i) => `e${i}`))
  renderBar({ selected })
  await userEvent.selectOptions(screen.getByLabelText('Status'), 'shelved')
  expect(screen.getByRole('button', { name: 'Apply' })).toBeDisabled()
  expect(screen.getByText('Selection is over the 200-entry limit.')).toBeInTheDocument()
})

it('sends only the checked ids under add_tag_ids, entry_ids from the selection', async () => {
  const fetchMock = vi.fn().mockResolvedValue(jsonResponse(200, { updated_count: 2 }))
  vi.stubGlobal('fetch', fetchMock)
  renderBar()
  await userEvent.click(within(screen.getByRole('group', { name: 'Add tags' })).getByRole('checkbox', { name: 'rpg' }))
  await userEvent.click(screen.getByRole('button', { name: 'Apply' }))
  await waitFor(() => expect(fetchMock).toHaveBeenCalled())
  const body = await putBody<BulkUpdateRequest>(fetchMock.mock.calls[0][0])
  expect(body).toEqual({ entry_ids: ['e1', 'e2'], add_tag_ids: ['t1'] })
})

it('sends only the checked ids under remove_tag_ids', async () => {
  const fetchMock = vi.fn().mockResolvedValue(jsonResponse(200, { updated_count: 2 }))
  vi.stubGlobal('fetch', fetchMock)
  renderBar()
  await userEvent.click(within(screen.getByRole('group', { name: 'Remove tags' })).getByRole('checkbox', { name: 'snes' }))
  await userEvent.click(screen.getByRole('button', { name: 'Apply' }))
  await waitFor(() => expect(fetchMock).toHaveBeenCalled())
  const body = await putBody<BulkUpdateRequest>(fetchMock.mock.calls[0][0])
  expect(body).toEqual({ entry_ids: ['e1', 'e2'], remove_tag_ids: ['t2'] })
})

it('unchecking an already-checked add-tag chip drops it from the request body', async () => {
  const fetchMock = vi.fn().mockResolvedValue(jsonResponse(200, { updated_count: 2 }))
  vi.stubGlobal('fetch', fetchMock)
  renderBar()
  const addGroup = within(screen.getByRole('group', { name: 'Add tags' }))
  await userEvent.click(addGroup.getByRole('checkbox', { name: 'rpg' }))
  await userEvent.click(addGroup.getByRole('checkbox', { name: 'snes' }))
  await userEvent.click(addGroup.getByRole('checkbox', { name: 'rpg' })) // toggled back off
  expect(addGroup.getByRole('checkbox', { name: 'rpg' })).not.toBeChecked()
  await userEvent.click(screen.getByRole('button', { name: 'Apply' }))
  await waitFor(() => expect(fetchMock).toHaveBeenCalled())
  const body = await putBody<BulkUpdateRequest>(fetchMock.mock.calls[0][0])
  expect(body).toEqual({ entry_ids: ['e1', 'e2'], add_tag_ids: ['t2'] })
})

it('unchecking an already-checked remove-tag chip drops it from the request body', async () => {
  const fetchMock = vi.fn().mockResolvedValue(jsonResponse(200, { updated_count: 2 }))
  vi.stubGlobal('fetch', fetchMock)
  renderBar()
  const removeGroup = within(screen.getByRole('group', { name: 'Remove tags' }))
  await userEvent.click(removeGroup.getByRole('checkbox', { name: 'rpg' }))
  await userEvent.click(removeGroup.getByRole('checkbox', { name: 'snes' }))
  await userEvent.click(removeGroup.getByRole('checkbox', { name: 'snes' })) // toggled back off
  expect(removeGroup.getByRole('checkbox', { name: 'snes' })).not.toBeChecked()
  await userEvent.click(screen.getByRole('button', { name: 'Apply' }))
  await waitFor(() => expect(fetchMock).toHaveBeenCalled())
  const body = await putBody<BulkUpdateRequest>(fetchMock.mock.calls[0][0])
  expect(body).toEqual({ entry_ids: ['e1', 'e2'], remove_tag_ids: ['t1'] })
})

it('checking a tag under Remove after it is checked under Add un-checks it there, and vice versa', async () => {
  renderBar()
  const addGroup = within(screen.getByRole('group', { name: 'Add tags' }))
  const removeGroup = within(screen.getByRole('group', { name: 'Remove tags' }))

  await userEvent.click(addGroup.getByRole('checkbox', { name: 'rpg' }))
  expect(addGroup.getByRole('checkbox', { name: 'rpg' })).toBeChecked()

  await userEvent.click(removeGroup.getByRole('checkbox', { name: 'rpg' }))
  expect(removeGroup.getByRole('checkbox', { name: 'rpg' })).toBeChecked()
  expect(addGroup.getByRole('checkbox', { name: 'rpg' })).not.toBeChecked()

  await userEvent.click(addGroup.getByRole('checkbox', { name: 'rpg' }))
  expect(addGroup.getByRole('checkbox', { name: 'rpg' })).toBeChecked()
  expect(removeGroup.getByRole('checkbox', { name: 'rpg' })).not.toBeChecked()
})

it('sends the chosen status', async () => {
  const fetchMock = vi.fn().mockResolvedValue(jsonResponse(200, { updated_count: 2 }))
  vi.stubGlobal('fetch', fetchMock)
  renderBar()
  await userEvent.selectOptions(screen.getByLabelText('Status'), 'shelved')
  await userEvent.click(screen.getByRole('button', { name: 'Apply' }))
  await waitFor(() => expect(fetchMock).toHaveBeenCalled())
  const body = await putBody<BulkUpdateRequest>(fetchMock.mock.calls[0][0])
  expect(body).toEqual({ entry_ids: ['e1', 'e2'], status: 'shelved' })
})

it('omits storage_location entirely when Set storage location stays unchecked', async () => {
  const fetchMock = vi.fn().mockResolvedValue(jsonResponse(200, { updated_count: 2 }))
  vi.stubGlobal('fetch', fetchMock)
  renderBar()
  await userEvent.selectOptions(screen.getByLabelText('Status'), 'shelved') // an unrelated action so Apply is enabled
  await userEvent.click(screen.getByRole('button', { name: 'Apply' }))
  await waitFor(() => expect(fetchMock).toHaveBeenCalled())
  const body = await putBody<BulkUpdateRequest>(fetchMock.mock.calls[0][0])
  expect(body).not.toHaveProperty('storage_location')
})

it('sends an explicit empty string when checked with no text - clears the field', async () => {
  const fetchMock = vi.fn().mockResolvedValue(jsonResponse(200, { updated_count: 2 }))
  vi.stubGlobal('fetch', fetchMock)
  renderBar()
  await userEvent.click(screen.getByRole('checkbox', { name: 'Set storage location' }))
  expect(screen.getByText('Empty clears the location.')).toBeInTheDocument()
  await userEvent.click(screen.getByRole('button', { name: 'Apply' }))
  await waitFor(() => expect(fetchMock).toHaveBeenCalled())
  const body = await putBody<BulkUpdateRequest>(fetchMock.mock.calls[0][0])
  expect(body).toEqual({ entry_ids: ['e1', 'e2'], storage_location: '' })
})

it('sends the typed text when checked with a value', async () => {
  const fetchMock = vi.fn().mockResolvedValue(jsonResponse(200, { updated_count: 2 }))
  vi.stubGlobal('fetch', fetchMock)
  renderBar()
  await userEvent.click(screen.getByRole('checkbox', { name: 'Set storage location' }))
  await userEvent.type(screen.getByLabelText('Storage location'), 'Shelf A')
  await userEvent.click(screen.getByRole('button', { name: 'Apply' }))
  await waitFor(() => expect(fetchMock).toHaveBeenCalled())
  const body = await putBody<BulkUpdateRequest>(fetchMock.mock.calls[0][0])
  expect(body).toEqual({ entry_ids: ['e1', 'e2'], storage_location: 'Shelf A' })
})

it('disables every control while the apply is pending', async () => {
  vi.stubGlobal('fetch', vi.fn().mockImplementation(() => new Promise(() => {})))
  renderBar()
  await userEvent.selectOptions(screen.getByLabelText('Status'), 'shelved')
  await userEvent.click(screen.getByRole('button', { name: 'Apply' }))
  expect(screen.getByRole('button', { name: 'Apply' })).toBeDisabled()
  expect(screen.getByRole('button', { name: 'Cancel' })).toBeDisabled()
  expect(screen.getByLabelText('Status')).toBeDisabled()
  expect(
    within(screen.getByRole('group', { name: 'Add tags' })).getByRole('checkbox', { name: 'rpg' }),
  ).toBeDisabled()
})

it('invalidates entries, tags, dashboard, and recommendations, and reports the updated count on success', async () => {
  vi.stubGlobal('fetch', vi.fn().mockResolvedValue(jsonResponse(200, { updated_count: 2 })))
  const { invalidateSpy, onApplied } = renderBar()
  await userEvent.selectOptions(screen.getByLabelText('Status'), 'shelved')
  await userEvent.click(screen.getByRole('button', { name: 'Apply' }))
  await waitFor(() => expect(onApplied).toHaveBeenCalledWith(2))
  const keys = invalidateSpy.mock.calls.map((c) => (c[0] as { queryKey: unknown[] }).queryKey)
  expect(keys).toContainEqual(['entries'])
  expect(keys).toContainEqual(['tags'])
  expect(keys).toContainEqual(['dashboard'])
  expect(keys).toContainEqual(['recommendations'])
})

it('shows the curated tag_cap_exceeded message and keeps the draft and selection for a retry, without exiting', async () => {
  vi.stubGlobal('fetch', vi.fn().mockResolvedValue(problemResponse(400, 'tag_cap_exceeded', 'entry would exceed 50 tags')))
  const { onApplied, onCancel } = renderBar()
  const addGroup = within(screen.getByRole('group', { name: 'Add tags' }))
  await userEvent.click(addGroup.getByRole('checkbox', { name: 'rpg' }))
  await userEvent.click(screen.getByRole('button', { name: 'Apply' }))
  expect(await screen.findByRole('alert')).toHaveTextContent(
    'One or more of the selected entries would end up with too many tags.',
  )
  expect(onApplied).not.toHaveBeenCalled()
  expect(onCancel).not.toHaveBeenCalled()
  expect(addGroup.getByRole('checkbox', { name: 'rpg' })).toBeChecked()
})

it('shows the generic fallback message when the failure is not a problem-bodied ApiError', async () => {
  vi.stubGlobal('fetch', vi.fn().mockRejectedValue(new TypeError('Failed to fetch')))
  const { onApplied, onCancel } = renderBar()
  await userEvent.selectOptions(screen.getByLabelText('Status'), 'shelved')
  await userEvent.click(screen.getByRole('button', { name: 'Apply' }))
  expect(await screen.findByRole('alert')).toHaveTextContent('The bulk update failed.')
  expect(onApplied).not.toHaveBeenCalled()
  expect(onCancel).not.toHaveBeenCalled()
})

it('Cancel calls onCancel', async () => {
  const { onCancel } = renderBar()
  await userEvent.click(screen.getByRole('button', { name: 'Cancel' }))
  expect(onCancel).toHaveBeenCalledTimes(1)
})
