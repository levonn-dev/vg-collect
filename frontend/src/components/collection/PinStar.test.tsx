import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { calledPath, entryFixture, jsonResponse, putBody } from '../../test/fixtures'
import { renderWithI18n } from '../../test/i18n'
import PinStar from './PinStar'

afterEach(() => vi.unstubAllGlobals())

it('PUTs the full baseline with pinned flipped', async () => {
  const e = entryFixture({
    pinned: false, notes: 'keep me', pricing_mode: 'proxy', pricing_product_id: 'p9',
    tags: [{ id: 't1', name: 'rpg' }],
  })
  const fetchMock = vi.fn().mockResolvedValue(jsonResponse(200, { ...e, pinned: true }))
  vi.stubGlobal('fetch', fetchMock)
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } })
  renderWithI18n(
    <QueryClientProvider client={qc}>
      <PinStar entry={e} />
    </QueryClientProvider>,
  )
  await userEvent.click(screen.getByRole('button', { name: 'Pin' }))
  expect(fetchMock).toHaveBeenCalledTimes(1)
  expect(calledPath(fetchMock, 0)).toBe(`/api/entries/${e.id}`)
  const body = await putBody<Record<string, unknown>>(fetchMock.mock.calls[0][0])
  expect(body.pinned).toBe(true)
  // The one-field toggle must not strip the rest of the entry.
  expect(body.notes).toBe('keep me')
  expect(body.pricing_mode).toBe('proxy')
  expect(body.tag_ids).toEqual(['t1'])
})

it('syncs the entry-detail cache with the updated entry (no stale write-back)', async () => {
  const e = entryFixture({ pinned: false })
  const updated = { ...e, pinned: true }
  vi.stubGlobal('fetch', vi.fn().mockResolvedValue(jsonResponse(200, updated)))
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } })
  qc.setQueryData(['entry', e.id], { ...e, notes: 'stale from before the pin' })
  renderWithI18n(
    <QueryClientProvider client={qc}>
      <PinStar entry={e} />
    </QueryClientProvider>,
  )
  await userEvent.click(screen.getByRole('button', { name: 'Pin' }))
  await waitFor(() => expect(qc.getQueryData(['entry', e.id])).toEqual(updated))
})

it('reads as Unpin when pinned', () => {
  const qc = new QueryClient()
  renderWithI18n(
    <QueryClientProvider client={qc}>
      <PinStar entry={entryFixture({ pinned: true })} />
    </QueryClientProvider>,
  )
  expect(screen.getByRole('button', { name: 'Unpin', pressed: true })).toBeInTheDocument()
})
