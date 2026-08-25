import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { calledPath, jsonResponse, problemResponse } from '../../test/fixtures'
import { renderWithI18n } from '../../test/i18n'
import ApprovalNotice from './ApprovalNotice'

function renderNotice() {
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false }, mutations: { retry: false } } })
  const view = renderWithI18n(
    <QueryClientProvider client={qc}>
      <ApprovalNotice entryId="e1" />
    </QueryClientProvider>,
  )
  return { ...view, qc }
}

afterEach(() => vi.unstubAllGlobals())

const sub = (extra: object) =>
  jsonResponse(200, { id: 's1', entry_id: 'e1', status: 'approved', created_at: 'x', updated_at: 'x', ...extra })

it('shows the banner for an approved, un-acked submission and acks on dismiss', async () => {
  const fetchMock = vi
    .fn()
    .mockResolvedValueOnce(sub({}))
    .mockResolvedValueOnce(new Response(null, { status: 204 }))
    // Successful ack invalidates ['submission', entryId], so a third call
    // (background refetch) follows; the acked submission answers from here on.
    .mockResolvedValue(sub({ resolution_ack_at: '2026-07-19T00:00:00Z' }))
  vi.stubGlobal('fetch', fetchMock)
  renderNotice()
  const dismiss = await screen.findByRole('button', { name: 'Dismiss approval notice' })
  await userEvent.click(dismiss)
  expect(calledPath(fetchMock, 1)).toBe('/api/entries/e1/submission/ack')
  expect((fetchMock.mock.calls[1][0] as Request).method).toBe('POST')
  expect(screen.queryByRole('status')).not.toBeInTheDocument()
})

it('re-shows the banner when the ack request fails', async () => {
  const fetchMock = vi
    .fn()
    .mockResolvedValueOnce(sub({}))
    .mockResolvedValue(problemResponse(500, 'internal', 'x'))
  vi.stubGlobal('fetch', fetchMock)
  renderNotice()
  const dismiss = await screen.findByRole('button', { name: 'Dismiss approval notice' })
  await userEvent.click(dismiss)
  // onError resets dismissed to false; cache was never invalidated, so the
  // still-un-acked submission renders again.
  expect(await screen.findByRole('status')).toBeInTheDocument()
  expect(await screen.findByRole('button', { name: 'Dismiss approval notice' })).toBeInTheDocument()
})

it('a successful ack invalidates the cache so a remount does not re-flash the banner', async () => {
  const fetchMock = vi
    .fn()
    .mockResolvedValueOnce(sub({}))
    .mockResolvedValueOnce(new Response(null, { status: 204 }))
    .mockResolvedValue(sub({ resolution_ack_at: '2026-07-19T00:00:00Z' }))
  vi.stubGlobal('fetch', fetchMock)
  const { unmount, qc } = renderNotice()
  const dismiss = await screen.findByRole('button', { name: 'Dismiss approval notice' })
  await userEvent.click(dismiss)
  // Lets the post-ack invalidation's background refetch land before remount,
  // so the remount reads from cache, not component state.
  await new Promise((r) => setTimeout(r, 0))
  unmount()

  renderWithI18n(
    <QueryClientProvider client={qc}>
      <ApprovalNotice entryId="e1" />
    </QueryClientProvider>,
  )
  // No await: a fresh mount's first synchronous render reads the cache as-is.
  // Without the onSuccess invalidation, the cache would still hold the
  // un-acked submission and flash the banner (refetch-on-mount fixes it later).
  expect(screen.queryByRole('status')).not.toBeInTheDocument()
})

it('stays hidden when already acknowledged', async () => {
  vi.stubGlobal('fetch', vi.fn().mockResolvedValue(sub({ resolution_ack_at: '2026-07-19T00:00:00Z' })))
  renderNotice()
  // Let the query settle, then confirm no banner.
  await new Promise((r) => setTimeout(r, 0))
  expect(screen.queryByRole('status')).not.toBeInTheDocument()
})

it('stays hidden with no submission (404)', async () => {
  vi.stubGlobal('fetch', vi.fn().mockResolvedValue(problemResponse(404, 'submission_not_found', 'x')))
  renderNotice()
  await new Promise((r) => setTimeout(r, 0))
  expect(screen.queryByRole('status')).not.toBeInTheDocument()
})
