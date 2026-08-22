import { i18n } from '@lingui/core'
import { I18nProvider } from '@lingui/react'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { render, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { calledPath } from '../../test/fixtures'
import FollowButton from './FollowButton'

afterEach(() => vi.unstubAllGlobals())

// Wrapped inline (not via the renderWithI18n helper): the first test
// below calls rerender() with a fresh element tree, which must carry
// the same I18nProvider ancestor as the initial render, or React would
// swap out the whole tree - including the i18n context - rather than
// reconcile it in place.
function renderButton(qc: QueryClient, viewerFollows: boolean) {
  return render(
    <I18nProvider i18n={i18n}>
      <QueryClientProvider client={qc}>
        <FollowButton userId="u1" handle="Alice_Prime" viewerFollows={viewerFollows} />
      </QueryClientProvider>
    </I18nProvider>,
  )
}

it('PUTs to follow, then DELETEs to unfollow once the caller re-renders with the new state', async () => {
  const fetchMock = vi.fn().mockResolvedValue(new Response(null, { status: 204 }))
  vi.stubGlobal('fetch', fetchMock)
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } })
  const { rerender } = renderButton(qc, false)
  await userEvent.click(screen.getByRole('button', { name: 'Follow' }))
  expect(calledPath(fetchMock)).toBe('/api/social/follows/u1')
  expect((fetchMock.mock.calls.at(-1)?.[0] as Request).method).toBe('PUT')

  rerender(
    <I18nProvider i18n={i18n}>
      <QueryClientProvider client={qc}>
        <FollowButton userId="u1" handle="Alice_Prime" viewerFollows />
      </QueryClientProvider>
    </I18nProvider>,
  )
  await userEvent.click(screen.getByRole('button', { name: 'Following' }))
  expect(calledPath(fetchMock)).toBe('/api/social/follows/u1')
  expect((fetchMock.mock.calls.at(-1)?.[0] as Request).method).toBe('DELETE')
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
