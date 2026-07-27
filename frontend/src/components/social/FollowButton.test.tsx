import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { render, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import FollowButton from './FollowButton'

afterEach(() => vi.unstubAllGlobals())

function renderButton(qc: QueryClient, viewerFollows: boolean) {
  return render(
    <QueryClientProvider client={qc}>
      <FollowButton userId="u1" handle="Alice_Prime" viewerFollows={viewerFollows} />
    </QueryClientProvider>,
  )
}

it('PUTs to follow, then DELETEs to unfollow once the caller re-renders with the new state', async () => {
  const fetchMock = vi.fn().mockResolvedValue(new Response(null, { status: 204 }))
  vi.stubGlobal('fetch', fetchMock)
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } })
  const { rerender } = renderButton(qc, false)
  await userEvent.click(screen.getByRole('button', { name: 'Follow' }))
  expect(fetchMock).toHaveBeenLastCalledWith('/api/social/follows/u1', { method: 'PUT' })

  rerender(
    <QueryClientProvider client={qc}>
      <FollowButton userId="u1" handle="Alice_Prime" viewerFollows />
    </QueryClientProvider>,
  )
  await userEvent.click(screen.getByRole('button', { name: 'Following' }))
  expect(fetchMock).toHaveBeenLastCalledWith('/api/social/follows/u1', { method: 'DELETE' })
})

it('invalidates the folded profile query key on success', async () => {
  vi.stubGlobal('fetch', vi.fn().mockResolvedValue(new Response(null, { status: 204 })))
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } })
  const invalidateSpy = vi.spyOn(qc, 'invalidateQueries')
  renderButton(qc, false)
  await userEvent.click(screen.getByRole('button', { name: 'Follow' }))
  await waitFor(() =>
    expect(invalidateSpy).toHaveBeenCalledWith({ queryKey: ['profile', 'aliceprime'] }),
  )
})

it('reads as Following, aria-pressed, when the viewer already follows', () => {
  const qc = new QueryClient()
  renderButton(qc, true)
  expect(screen.getByRole('button', { name: 'Following', pressed: true })).toBeInTheDocument()
})
