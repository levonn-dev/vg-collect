import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { screen, waitFor, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import type { PromoteCandidatesPage } from '../../api/admin'
import type { Product } from '../../api/catalog'
import { calledPath, jsonResponse, problemResponse, putBody, requestPath } from '../../test/fixtures'
import { renderWithI18n } from '../../test/i18n'
import PromotePanel from './PromotePanel'

type Candidate = PromoteCandidatesPage['products'][number]['candidates'][number]

function renderPanel(product: Product, candidates: Candidate[], onDone = vi.fn()) {
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false }, mutations: { retry: false } } })
  renderWithI18n(
    <QueryClientProvider client={qc}>
      <PromotePanel product={product} candidates={candidates} onDone={onDone} />
    </QueryClientProvider>,
  )
  return onDone
}

afterEach(() => {
  vi.unstubAllGlobals()
  vi.restoreAllMocks()
})

const communityGame: Product = {
  id: 'p1', type: 'game', name: 'Repro Alpha', origin: 'community',
  community: { platform_name: 'SNES' }, created_at: 'x', updated_at: 'x',
}
const candidate: Candidate = { provider: 'igdb', provider_id: 1011, name: 'Chrono Trigger', score: 0.92, found_at: 'x' }

it('game promote picks via search and posts provider identity', async () => {
  vi.spyOn(window, 'confirm').mockReturnValue(true)
  const fetchMock = vi.fn().mockImplementation((url: unknown) => {
    if (requestPath(url).startsWith('/api/search'))
      return Promise.resolve(jsonResponse(200, {
        degraded: false,
        results: [{ type: 'game', name: 'Chrono Trigger', igdb_game_id: 1011,
          platforms: [{ igdb_platform_id: 19, name: 'SNES' }] }],
      }))
    return Promise.resolve(jsonResponse(200, { ...communityGame, origin: undefined, igdb: { game_id: 1011 } }))
  })
  vi.stubGlobal('fetch', fetchMock)
  const onDone = vi.fn()
  renderPanel(communityGame, [candidate], onDone)
  await userEvent.click(screen.getByRole('button', { name: 'Promote to provider identity' }))
  await userEvent.click(await screen.findByRole('button', { name: 'Search' }))
  await userEvent.click(await screen.findByRole('button', { name: 'Chrono Trigger on SNES' }))
  const promoteCall = fetchMock.mock.calls.find((c) => requestPath(c[0]).endsWith('/promote'))
  expect(requestPath(promoteCall?.[0])).toBe('/api/admin/products/p1/promote')
  expect(await putBody(promoteCall?.[0])).toEqual({ igdb_game_id: 1011, platform_igdb_id: 19 })
  expect(onDone).toHaveBeenCalled()
})

it('game promote with an attached listing posts all three ids', async () => {
  vi.spyOn(window, 'confirm').mockReturnValue(true)
  const fetchMock = vi.fn().mockImplementation((url: unknown) => {
    if (requestPath(url).includes('type=pc_listing'))
      return Promise.resolve(jsonResponse(200, {
        degraded: false,
        results: [{ type: 'pc_listing', name: 'Chrono Trigger Listing', pc_product_id: 7788 }],
      }))
    if (requestPath(url).startsWith('/api/search'))
      return Promise.resolve(jsonResponse(200, {
        degraded: false,
        results: [{ type: 'game', name: 'Chrono Trigger', igdb_game_id: 1011,
          platforms: [{ igdb_platform_id: 19, name: 'SNES' }] }],
      }))
    return Promise.resolve(jsonResponse(200, { ...communityGame, origin: undefined, igdb: { game_id: 1011 } }))
  })
  vi.stubGlobal('fetch', fetchMock)
  const onDone = vi.fn()
  renderPanel(communityGame, [candidate], onDone)
  await userEvent.click(screen.getByRole('button', { name: 'Promote to provider identity' }))
  // Attach the listing first: the game pick fires the promote, so
  // pc_product_id must already be on state.
  await userEvent.click(screen.getByRole('button', { name: /attach a price listing/i }))
  const dialog = await screen.findByRole('dialog', { name: 'Match a price listing' })
  await userEvent.click(await within(dialog).findByRole('button', { name: 'Use Chrono Trigger Listing' }))
  await userEvent.click(await screen.findByRole('button', { name: 'Chrono Trigger on SNES' }))
  const promoteCall = fetchMock.mock.calls.find((c) => requestPath(c[0]).endsWith('/promote'))
  expect(requestPath(promoteCall?.[0])).toBe('/api/admin/products/p1/promote')
  expect(await putBody(promoteCall?.[0])).toEqual({
    igdb_game_id: 1011, platform_igdb_id: 19, pc_product_id: 7788,
  })
  expect(onDone).toHaveBeenCalled()
})

it('clearing an attached listing reverts the promote body to the two-id shape', async () => {
  vi.spyOn(window, 'confirm').mockReturnValue(true)
  const fetchMock = vi.fn().mockImplementation((url: unknown) => {
    if (requestPath(url).includes('type=pc_listing'))
      return Promise.resolve(jsonResponse(200, {
        degraded: false,
        results: [{ type: 'pc_listing', name: 'Chrono Trigger Listing', pc_product_id: 7788 }],
      }))
    if (requestPath(url).startsWith('/api/search'))
      return Promise.resolve(jsonResponse(200, {
        degraded: false,
        results: [{ type: 'game', name: 'Chrono Trigger', igdb_game_id: 1011,
          platforms: [{ igdb_platform_id: 19, name: 'SNES' }] }],
      }))
    return Promise.resolve(jsonResponse(200, { ...communityGame, origin: undefined, igdb: { game_id: 1011 } }))
  })
  vi.stubGlobal('fetch', fetchMock)
  const onDone = vi.fn()
  renderPanel(communityGame, [candidate], onDone)
  await userEvent.click(screen.getByRole('button', { name: 'Promote to provider identity' }))
  await userEvent.click(screen.getByRole('button', { name: /attach a price listing/i }))
  const dialog = await screen.findByRole('dialog', { name: 'Match a price listing' })
  await userEvent.click(await within(dialog).findByRole('button', { name: 'Use Chrono Trigger Listing' }))
  expect(screen.getByText('Listing: Chrono Trigger Listing')).toBeInTheDocument()
  // Clear drops the listing to null; the reappeared "Attach" button proves
  // the no-listing branch, so the promote body falls back to the two-id shape.
  await userEvent.click(screen.getByRole('button', { name: 'Clear' }))
  expect(screen.queryByText('Listing: Chrono Trigger Listing')).not.toBeInTheDocument()
  expect(screen.getByRole('button', { name: /attach a price listing/i })).toBeInTheDocument()
  await userEvent.click(await screen.findByRole('button', { name: 'Chrono Trigger on SNES' }))
  const promoteCall = fetchMock.mock.calls.find((c) => requestPath(c[0]).endsWith('/promote'))
  expect(requestPath(promoteCall?.[0])).toBe('/api/admin/products/p1/promote')
  expect(await putBody(promoteCall?.[0])).toEqual({ igdb_game_id: 1011, platform_igdb_id: 19 })
  expect(onDone).toHaveBeenCalled()
})

it('closing the attach dialog without a pick leaves no listing and posts the two-id shape', async () => {
  vi.spyOn(window, 'confirm').mockReturnValue(true)
  const fetchMock = vi.fn().mockImplementation((url: unknown) => {
    if (requestPath(url).includes('type=pc_listing'))
      return Promise.resolve(jsonResponse(200, { degraded: false, results: [] }))
    if (requestPath(url).startsWith('/api/search'))
      return Promise.resolve(jsonResponse(200, {
        degraded: false,
        results: [{ type: 'game', name: 'Chrono Trigger', igdb_game_id: 1011,
          platforms: [{ igdb_platform_id: 19, name: 'SNES' }] }],
      }))
    return Promise.resolve(jsonResponse(200, { ...communityGame, origin: undefined, igdb: { game_id: 1011 } }))
  })
  vi.stubGlobal('fetch', fetchMock)
  const onDone = vi.fn()
  renderPanel(communityGame, [candidate], onDone)
  await userEvent.click(screen.getByRole('button', { name: 'Promote to provider identity' }))
  await userEvent.click(screen.getByRole('button', { name: /attach a price listing/i }))
  const dialog = await screen.findByRole('dialog', { name: 'Match a price listing' })
  await userEvent.click(within(dialog).getByRole('button', { name: 'Close' }))
  expect(screen.queryByRole('dialog', { name: 'Match a price listing' })).not.toBeInTheDocument()
  expect(screen.queryByText(/^Listing:/)).not.toBeInTheDocument()
  expect(screen.getByRole('button', { name: /attach a price listing/i })).toBeInTheDocument()
  await userEvent.click(await screen.findByRole('button', { name: 'Chrono Trigger on SNES' }))
  const promoteCall = fetchMock.mock.calls.find((c) => requestPath(c[0]).endsWith('/promote'))
  expect(requestPath(promoteCall?.[0])).toBe('/api/admin/products/p1/promote')
  expect(await putBody(promoteCall?.[0])).toEqual({ igdb_game_id: 1011, platform_igdb_id: 19 })
  expect(onDone).toHaveBeenCalled()
})

it('renders the identity_taken holder detail verbatim', async () => {
  vi.spyOn(window, 'confirm').mockReturnValue(true)
  const fetchMock = vi.fn().mockImplementation((url: unknown) => {
    if (requestPath(url).startsWith('/api/search'))
      return Promise.resolve(jsonResponse(200, {
        degraded: false,
        results: [{ type: 'game', name: 'Chrono Trigger', igdb_game_id: 1011,
          platforms: [{ igdb_platform_id: 19, name: 'SNES' }] }],
      }))
    return Promise.resolve(problemResponse(
      409,
      'identity_taken',
      'another product with the same identity already carries that listing (holder: 8563fd43 "Tony Hawk\'s Pro Skater")',
    ))
  })
  vi.stubGlobal('fetch', fetchMock)
  renderPanel(communityGame, [candidate])
  await userEvent.click(screen.getByRole('button', { name: 'Promote to provider identity' }))
  await userEvent.click(await screen.findByRole('button', { name: 'Search' }))
  await userEvent.click(await screen.findByRole('button', { name: 'Chrono Trigger on SNES' }))
  const alert = await screen.findByRole('alert')
  expect(alert).toHaveTextContent(/holder: 8563fd43/i)
})

// identity_taken prefers e.message, but only when the server sent one; an
// empty detail falls through to the fixed text, not a blank alert.
it('falls back to the fixed identity_taken text when the server sends no detail', async () => {
  vi.spyOn(window, 'confirm').mockReturnValue(true)
  const fetchMock = vi.fn().mockImplementation((url: unknown) => {
    if (requestPath(url).startsWith('/api/search'))
      return Promise.resolve(jsonResponse(200, {
        degraded: false,
        results: [{ type: 'game', name: 'Chrono Trigger', igdb_game_id: 1011,
          platforms: [{ igdb_platform_id: 19, name: 'SNES' }] }],
      }))
    return Promise.resolve(problemResponse(409, 'identity_taken', ''))
  })
  vi.stubGlobal('fetch', fetchMock)
  renderPanel(communityGame, [candidate])
  await userEvent.click(screen.getByRole('button', { name: 'Promote to provider identity' }))
  await userEvent.click(await screen.findByRole('button', { name: 'Search' }))
  await userEvent.click(await screen.findByRole('button', { name: 'Chrono Trigger on SNES' }))
  const alert = await screen.findByRole('alert')
  expect(alert).toHaveTextContent('A provider product already holds that identity - nothing changed.')
})

it('hardware promote picks via manual match and posts pc_product_id only', async () => {
  vi.spyOn(window, 'confirm').mockReturnValue(true)
  const communityAccessory: Product = {
    id: 'p3', type: 'accessory', name: 'Zapper', origin: 'community',
    created_at: 'x', updated_at: 'x',
  }
  const hwCandidate: Candidate = { provider: 'pricecharting', provider_id: 3033, name: 'NES Zapper', score: 0.88, found_at: 'x' }
  const fetchMock = vi.fn().mockImplementation((url: unknown) => {
    if (requestPath(url).includes('type=pc_listing'))
      return Promise.resolve(jsonResponse(200, {
        degraded: false,
        results: [{ type: 'pc_listing', name: 'NES Zapper Listing', pc_product_id: 9099 }],
      }))
    return Promise.resolve(jsonResponse(200, { ...communityAccessory, origin: undefined, pricecharting: { pc_product_id: 9099 } }))
  })
  vi.stubGlobal('fetch', fetchMock)
  const onDone = vi.fn()
  renderPanel(communityAccessory, [hwCandidate], onDone)
  await userEvent.click(screen.getByRole('button', { name: 'Promote to provider identity' }))
  const dialog = await screen.findByRole('dialog', { name: 'Match a price listing' })
  await userEvent.click(await within(dialog).findByRole('button', { name: 'Use NES Zapper Listing' }))
  const promoteCall = fetchMock.mock.calls.find((c) => requestPath(c[0]).endsWith('/promote'))
  expect(requestPath(promoteCall?.[0])).toBe('/api/admin/products/p3/promote')
  expect(await putBody(promoteCall?.[0])).toEqual({ pc_product_id: 9099 })
  expect(onDone).toHaveBeenCalled()
})

it('confirm-declined does not promote and leaves the panel open', async () => {
  vi.spyOn(window, 'confirm').mockReturnValue(false)
  const fetchMock = vi.fn().mockImplementation((url: unknown) => {
    if (requestPath(url).startsWith('/api/search'))
      return Promise.resolve(jsonResponse(200, {
        degraded: false,
        results: [{ type: 'game', name: 'Chrono Trigger', igdb_game_id: 1011,
          platforms: [{ igdb_platform_id: 19, name: 'SNES' }] }],
      }))
    return Promise.resolve(jsonResponse(200, { ...communityGame, origin: undefined, igdb: { game_id: 1011 } }))
  })
  vi.stubGlobal('fetch', fetchMock)
  const onDone = vi.fn()
  renderPanel(communityGame, [candidate], onDone)
  await userEvent.click(screen.getByRole('button', { name: 'Promote to provider identity' }))
  await userEvent.click(await screen.findByRole('button', { name: 'Search' }))
  await userEvent.click(await screen.findByRole('button', { name: 'Chrono Trigger on SNES' }))
  expect(fetchMock.mock.calls.some((c) => requestPath(c[0]).endsWith('/promote'))).toBe(false)
  expect(onDone).not.toHaveBeenCalled()
  expect(screen.getByLabelText('Promote Repro Alpha')).toBeInTheDocument()
})

it('dismiss posts the pair and keeps the panel open', async () => {
  const fetchMock = vi.fn().mockResolvedValue(new Response(null, { status: 204 }))
  vi.stubGlobal('fetch', fetchMock)
  const second: Candidate = { provider: 'igdb', provider_id: 2022, name: 'Secret of Mana', score: 0.8, found_at: 'x' }
  const onDone = renderPanel(communityGame, [candidate, second])
  await userEvent.click(screen.getAllByRole('button', { name: 'Dismiss' })[0])
  expect(calledPath(fetchMock, 0)).toBe('/api/admin/products/p1/promote-candidates/dismiss')
  expect(await putBody(fetchMock.mock.calls[0][0])).toEqual({ provider: 'igdb', provider_id: 1011 })
  // Waits for the mutation to settle (button re-enables), then proves dismiss
  // did not close the panel while other candidates remain.
  await waitFor(() => expect(screen.getAllByRole('button', { name: 'Dismiss' })[0]).not.toBeDisabled())
  expect(onDone).not.toHaveBeenCalled()
  expect(screen.getByLabelText('Promote Repro Alpha')).toBeInTheDocument()
})
