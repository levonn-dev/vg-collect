import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { renderWithI18n } from '../../test/i18n'
import LikeButton from './LikeButton'

afterEach(() => vi.unstubAllGlobals())

function renderButton(qc: QueryClient, viewerLikes: boolean, count = 3) {
  return renderWithI18n(
    <QueryClientProvider client={qc}>
      <LikeButton shelfId="s1" viewerLikes={viewerLikes} count={count} />
    </QueryClientProvider>,
  )
}

it('renders the given count and PUTs to like', async () => {
  const fetchMock = vi.fn().mockResolvedValue(new Response(null, { status: 204 }))
  vi.stubGlobal('fetch', fetchMock)
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } })
  renderButton(qc, false, 3)
  expect(screen.getByText('3')).toBeInTheDocument()
  await userEvent.click(screen.getByRole('button', { name: 'Like' }))
  expect(fetchMock).toHaveBeenLastCalledWith('/api/social/likes/s1', { method: 'PUT' })
})

it('DELETEs to unlike when the viewer already likes it, without moving the count itself', async () => {
  const fetchMock = vi.fn().mockResolvedValue(new Response(null, { status: 204 }))
  vi.stubGlobal('fetch', fetchMock)
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } })
  renderButton(qc, true, 4)
  await userEvent.click(screen.getByRole('button', { name: 'Unlike' }))
  expect(fetchMock).toHaveBeenLastCalledWith('/api/social/likes/s1', { method: 'DELETE' })
  // optimistic-free: the prop-driven count does not move on its own,
  // only once the invalidated query re-fetches with a real one.
  expect(screen.getByText('4')).toBeInTheDocument()
})

it('invalidates the shelf summary and the shared-shelf query family on success', async () => {
  vi.stubGlobal('fetch', vi.fn().mockResolvedValue(new Response(null, { status: 204 })))
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } })
  const invalidateSpy = vi.spyOn(qc, 'invalidateQueries')
  renderButton(qc, false)
  await userEvent.click(screen.getByRole('button', { name: 'Like' }))
  await waitFor(() => {
    expect(invalidateSpy).toHaveBeenCalledWith({ queryKey: ['shelfSummary', 's1'] })
    expect(invalidateSpy).toHaveBeenCalledWith({ queryKey: ['sharedShelf'] })
  })
})

it('reads aria-pressed when the viewer already likes it', () => {
  const qc = new QueryClient()
  renderButton(qc, true)
  expect(screen.getByRole('button', { name: 'Unlike', pressed: true })).toBeInTheDocument()
})
