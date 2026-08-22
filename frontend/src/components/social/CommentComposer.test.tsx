import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { fireEvent, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { calledPath, jsonResponse, problemResponse } from '../../test/fixtures'
import { renderWithI18n } from '../../test/i18n'
import CommentComposer from './CommentComposer'

afterEach(() => vi.unstubAllGlobals())

function renderComposer() {
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } })
  const invalidateSpy = vi.spyOn(qc, 'invalidateQueries')
  renderWithI18n(
    <QueryClientProvider client={qc}>
      <CommentComposer shelfId="s1" />
    </QueryClientProvider>,
  )
  return { invalidateSpy }
}

it('disables submit until there is non-whitespace text, and shows a live length counter', async () => {
  renderComposer()
  const box = screen.getByRole('textbox', { name: 'Add a comment' })
  const submit = screen.getByRole('button', { name: 'Post' })
  expect(submit).toBeDisabled()
  expect(screen.getByText('0/2000')).toBeInTheDocument()

  await userEvent.type(box, '   ')
  expect(submit).toBeDisabled()

  await userEvent.type(box, 'Nice shelf!')
  expect(submit).toBeEnabled()
  expect(screen.getByText('14/2000')).toBeInTheDocument()
})

it('the submit handler itself guards against an empty body too, not just the disabled button', () => {
  const fetchMock = vi.fn()
  vi.stubGlobal('fetch', fetchMock)
  renderComposer()
  // Bypasses the disabled attribute by submitting the form directly -
  // proves canPost is re-checked inside onSubmit, not only relied on
  // via the button's disabled state.
  fireEvent.submit(screen.getByRole('textbox', { name: 'Add a comment' }).closest('form')!)
  expect(fetchMock).not.toHaveBeenCalled()
})

it('caps input at 2000 characters via maxLength', () => {
  renderComposer()
  expect(screen.getByRole('textbox', { name: 'Add a comment' })).toHaveAttribute('maxLength', '2000')
})

it('posts the trimmed body, clears the field, and invalidates the comment queries on success', async () => {
  const fetchMock = vi.fn().mockResolvedValue(
    jsonResponse(201, {
      id: 'c1', shelf_id: 's1', author_id: 'u1', body: 'Nice shelf!', created_at: '2026-07-25T00:00:00Z',
    }),
  )
  vi.stubGlobal('fetch', fetchMock)
  const { invalidateSpy } = renderComposer()
  const box = screen.getByRole('textbox', { name: 'Add a comment' })
  await userEvent.type(box, '  Nice shelf!  ')
  await userEvent.click(screen.getByRole('button', { name: 'Post' }))

  await waitFor(() => expect(box).toHaveValue(''))
  expect(calledPath(fetchMock, 0)).toBe('/api/shelves/s1/comments')
  const req = fetchMock.mock.calls[0][0] as Request
  expect(req.method).toBe('POST')
  expect(await req.text()).toBe(JSON.stringify({ body: 'Nice shelf!' }))
  expect(invalidateSpy).toHaveBeenCalledWith({ queryKey: ['shelfComments', 's1'] })
  expect(invalidateSpy).toHaveBeenCalledWith({ queryKey: ['shelfSummary', 's1'] })
})

it('shows the rate-limit message for a 429 and leaves the draft in place', async () => {
  vi.stubGlobal('fetch', vi.fn().mockResolvedValue(problemResponse(429, 'cap_exceeded')))
  renderComposer()
  const box = screen.getByRole('textbox', { name: 'Add a comment' })
  await userEvent.type(box, 'One more!')
  await userEvent.click(screen.getByRole('button', { name: 'Post' }))

  expect(await screen.findByRole('alert')).toHaveTextContent('Comment limit reached - try again later.')
  expect(box).toHaveValue('One more!')
})

it('shows a generic failure message for a non-429 error', async () => {
  vi.stubGlobal('fetch', vi.fn().mockResolvedValue(jsonResponse(502, {})))
  renderComposer()
  const box = screen.getByRole('textbox', { name: 'Add a comment' })
  await userEvent.type(box, 'One more!')
  await userEvent.click(screen.getByRole('button', { name: 'Post' }))

  expect(await screen.findByRole('alert')).toHaveTextContent(/could not be posted/i)
})
