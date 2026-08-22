import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { calledPath, jsonResponse, problemResponse } from '../../test/fixtures'
import { renderWithI18n } from '../../test/i18n'
import CatalogSubmission from './CatalogSubmission'

function renderBlock() {
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false }, mutations: { retry: false } } })
  return renderWithI18n(
    <QueryClientProvider client={qc}>
      <CatalogSubmission entryId="e1" />
    </QueryClientProvider>,
  )
}

afterEach(() => vi.unstubAllGlobals())

const sub = (status: string, extra: object = {}) =>
  jsonResponse(200, { id: 's1', entry_id: 'e1', status, created_at: 'x', updated_at: 'x', ...extra })

it('never-submitted offers Submit and posts', async () => {
  const fetchMock = vi
    .fn()
    .mockResolvedValueOnce(problemResponse(404, 'submission_not_found', 'none'))
    .mockResolvedValueOnce(jsonResponse(201, { id: 's1', entry_id: 'e1', status: 'pending', created_at: 'x', updated_at: 'x' }))
    .mockResolvedValue(sub('pending'))
  vi.stubGlobal('fetch', fetchMock)
  renderBlock()
  await userEvent.click(await screen.findByRole('button', { name: 'Submit to catalog' }))
  expect(calledPath(fetchMock, 1)).toBe('/api/entries/e1/submission')
  expect((fetchMock.mock.calls[1][0] as Request).method).toBe('POST')
  expect(await screen.findByRole('button', { name: 'Cancel submission' })).toBeInTheDocument()
})

it('pending shows the wait and cancels', async () => {
  const fetchMock = vi
    .fn()
    .mockResolvedValueOnce(sub('pending'))
    .mockResolvedValueOnce(new Response(null, { status: 204 }))
    .mockResolvedValue(sub('cancelled'))
  vi.stubGlobal('fetch', fetchMock)
  renderBlock()
  await userEvent.click(await screen.findByRole('button', { name: 'Cancel submission' }))
  expect((fetchMock.mock.calls[1][0] as Request).method).toBe('DELETE')
  expect(await screen.findByRole('button', { name: 'Submit to catalog' })).toBeInTheDocument()
})

it('rejected shows the reason and offers Resubmit', async () => {
  vi.stubGlobal('fetch', vi.fn().mockResolvedValue(sub('rejected', { reject_reason: 'not a shared item' })))
  renderBlock()
  expect(await screen.findByText(/not a shared item/)).toBeInTheDocument()
  expect(screen.getByRole('button', { name: 'Resubmit to catalog' })).toBeInTheDocument()
})

it('renders the 429 detail verbatim at the button', async () => {
  const fetchMock = vi
    .fn()
    .mockResolvedValueOnce(problemResponse(404, 'submission_not_found', 'none'))
    .mockResolvedValueOnce(problemResponse(429, 'submission_rate_limited', 'at most 20 submissions per rolling 24h; try again later'))
  vi.stubGlobal('fetch', fetchMock)
  renderBlock()
  await userEvent.click(await screen.findByRole('button', { name: 'Submit to catalog' }))
  expect(await screen.findByRole('alert')).toHaveTextContent('at most 20 submissions per rolling 24h; try again later')
})
