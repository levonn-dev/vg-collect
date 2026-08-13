import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { screen, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { MemoryRouter } from 'react-router'
import { jsonResponse, meFixture, problemResponse, putBody } from '../../test/fixtures'
import { renderWithI18n } from '../../test/i18n'
import { defaultListState, toViewParams } from '../../lib/listParams'
import ShelfManager from './ShelfManager'

const savedState = { ...defaultListState(), status: ['backlog' as const], mode: 'grid' as const }
const view = {
  id: 'v1', name: 'Backlog wall', slug: 'backlog-wall', visibility: 'private' as const,
  params: toViewParams(savedState),
  created_at: '2026-07-01T00:00:00Z', updated_at: '2026-07-01T00:00:00Z',
}

// ShelfManager reads ['me'] both for the note logic and the copy-link
// handle; seed it with meFixture's own default (a private profile) so
// a test only has to override profile_visibility when the scenario
// needs something else.
function renderManager(me = meFixture()) {
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false, staleTime: Infinity } } })
  qc.setQueryData(['me'], me)
  renderWithI18n(
    <QueryClientProvider client={qc}>
      <MemoryRouter>
        <ShelfManager />
      </MemoryRouter>
    </QueryClientProvider>,
  )
}

afterEach(() => {
  vi.unstubAllGlobals()
  vi.restoreAllMocks()
})

it('shows a visibility badge for every shelf', async () => {
  vi.stubGlobal('fetch', vi.fn().mockResolvedValue(jsonResponse(200, { views: [view] })))
  renderManager()
  expect(await screen.findByText('private')).toBeInTheDocument()
})

it('shows the copy-link button only for shelves that have left private', async () => {
  const listed = { ...view, id: 'v2', name: 'Public wall', slug: 'public-wall', visibility: 'listed' as const }
  vi.stubGlobal('fetch', vi.fn().mockResolvedValue(jsonResponse(200, { views: [view, listed] })))
  renderManager()
  await screen.findByText('listed')
  expect(screen.getAllByRole('button', { name: 'Copy link' })).toHaveLength(1)
})

it('copies the handle-and-slug share link for a non-private shelf', async () => {
  const listed = { ...view, visibility: 'listed' as const }
  vi.stubGlobal('fetch', vi.fn().mockResolvedValue(jsonResponse(200, { views: [listed] })))
  const writeText = vi.fn().mockResolvedValue(undefined)
  Object.defineProperty(navigator, 'clipboard', { value: { writeText }, configurable: true })
  renderManager()
  await userEvent.click(await screen.findByRole('button', { name: 'Copy link' }))
  expect(writeText).toHaveBeenCalledWith(`${window.location.origin}/u/${meFixture().handle}/shelves/${listed.slug}`)
})

it('withholds the copy-link button for a non-private shelf until the signed-in handle has loaded', async () => {
  const listed = { ...view, visibility: 'listed' as const }
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false, staleTime: Infinity } } })
  // No ['me'] seed here (unlike renderManager), and /api/me hangs - the
  // handle read is still pending, so there is nothing to build a share
  // link out of yet even though the shelf itself already qualifies.
  vi.stubGlobal('fetch', vi.fn((path: string) =>
    path === '/api/me' ? new Promise(() => {}) : Promise.resolve(jsonResponse(200, { views: [listed] }))))
  renderWithI18n(
    <QueryClientProvider client={qc}>
      <MemoryRouter>
        <ShelfManager />
      </MemoryRouter>
    </QueryClientProvider>,
  )
  await screen.findByText(listed.name)
  expect(screen.queryByRole('button', { name: 'Copy link' })).not.toBeInTheDocument()
})

it('clicking a row visibility segment calls updateView with that shelf\'s own name, params, and the new visibility', async () => {
  const fetchMock = vi.fn().mockImplementation((_url: string, init?: RequestInit) =>
    Promise.resolve(init?.method === 'PUT'
      ? jsonResponse(200, { ...view, visibility: 'listed' })
      : jsonResponse(200, { views: [view] })))
  vi.stubGlobal('fetch', fetchMock)
  renderManager()
  await screen.findByText(view.name)
  await userEvent.click(screen.getByRole('button', { name: 'Listed' }))
  const put = fetchMock.mock.calls.find((c) => (c[1] as RequestInit | undefined)?.method === 'PUT')
  expect(put?.[0]).toBe(`/api/views/${view.id}`)
  expect(putBody(put?.[1] as RequestInit)).toEqual({
    name: view.name, params: view.params, visibility: 'listed',
  })
})

it('surfaces a failed visibility change as an alert', async () => {
  const fetchMock = vi.fn().mockImplementation((_url: string, init?: RequestInit) =>
    Promise.resolve(init?.method === 'PUT'
      ? problemResponse(500, 'internal', 'view visibility update failed')
      : jsonResponse(200, { views: [view] })))
  vi.stubGlobal('fetch', fetchMock)
  renderManager()
  await screen.findByText(view.name)
  await userEvent.click(screen.getByRole('button', { name: 'Listed' }))
  expect(await screen.findByRole('alert')).toHaveTextContent(/visibility update failed/i)
})

it('shows the empty state when there are no shelves yet', async () => {
  vi.stubGlobal('fetch', vi.fn().mockResolvedValue(jsonResponse(200, { views: [] })))
  renderManager()
  expect(await screen.findByText('No shelves yet. Save one from the Items tab.')).toBeInTheDocument()
})

it('shows the private-profile note when the profile is private and at least one shelf is non-private, with a link to Account', async () => {
  const listed = { ...view, visibility: 'listed' as const }
  vi.stubGlobal('fetch', vi.fn().mockResolvedValue(jsonResponse(200, { views: [listed] })))
  renderManager(meFixture({ profile_visibility: 'private' }))
  const note = await screen.findByRole('note')
  expect(note).toHaveTextContent(/your profile is private/i)
  expect(note).toHaveTextContent(/change profile visibility/i)
  expect(within(note).getByRole('link', { name: 'in Account' })).toHaveAttribute('href', '/account')
})

it('shows the unlisted-profile note when the profile is unlisted and at least one shelf is listed', async () => {
  const listed = { ...view, visibility: 'listed' as const }
  vi.stubGlobal('fetch', vi.fn().mockResolvedValue(jsonResponse(200, { views: [listed] })))
  renderManager(meFixture({ profile_visibility: 'unlisted' }))
  const note = await screen.findByRole('note')
  expect(note).toHaveTextContent(/your profile is unlisted/i)
  expect(note).toHaveTextContent(/will not appear in explore/i)
})

it('shows no note when the profile is private but every shelf is also private', async () => {
  vi.stubGlobal('fetch', vi.fn().mockResolvedValue(jsonResponse(200, { views: [view] })))
  renderManager(meFixture({ profile_visibility: 'private' }))
  await screen.findByText(view.name)
  expect(screen.queryByRole('note')).not.toBeInTheDocument()
})

it('shows no note when the profile is listed', async () => {
  const listed = { ...view, visibility: 'listed' as const }
  vi.stubGlobal('fetch', vi.fn().mockResolvedValue(jsonResponse(200, { views: [listed] })))
  renderManager(meFixture({ profile_visibility: 'listed' }))
  await screen.findByText(listed.name)
  expect(screen.queryByRole('note')).not.toBeInTheDocument()
})
