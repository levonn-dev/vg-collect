import { i18n } from '@lingui/core'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { cleanup, screen, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import type { AdminSubmission } from '../../api/admin'
import type { Platform } from '../../api/platforms'
import { ApiError } from '../../api/client'
import { messages as jaMessages } from '../../locales/ja.po'
import { jsonResponse, problemResponse, putBody, requestPath } from '../../test/fixtures'
import { renderWithI18n } from '../../test/i18n'
import ReviewPanel from './ReviewPanel'

function renderPanel(submission: AdminSubmission, onDone = vi.fn(), platforms: Platform[] = []) {
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false, staleTime: Infinity }, mutations: { retry: false } } })
  // PlatformPicker renders unconditionally here (no custom-entry gate)
  // and fires a ['platforms'] fetch on mount; seed it fresh+stale-proof
  // so that call never races the verdict/adopt calls the tests assert on.
  // Empty by default (most tests never search the catalog); a test that
  // drives a confirmed pick supplies its own seed rows.
  qc.setQueryData(['platforms'], { platforms })
  renderWithI18n(
    <QueryClientProvider client={qc}>
      <ReviewPanel submission={submission} onDone={onDone} />
    </QueryClientProvider>,
  )
  return onDone
}

afterEach(() => {
  vi.unstubAllGlobals()
  // Order matters: cleanup() before activate() - see EntryDetail.test.tsx's
  // afterEach for why (I18nProvider update outside act otherwise).
  cleanup()
  i18n.activate('en')
})

const row: AdminSubmission = {
  id: 's1', entry_id: 'e1', user_id: 'u1', status: 'pending',
  display_name: 'repro alpha', item_type: 'game', platform_name: 'snes',
  region: 'pal', edition: 'glow cart', created_at: '2026-07-17T00:00:00Z', updated_at: '2026-07-17T00:00:00Z',
}

it('approve-new mints from the curated form', async () => {
  // mockImplementation (not a single shared mockResolvedValue object): the
  // panel's own duplicates search and the verdict call each need their own
  // fresh Response - a Response body can only be read once, and reusing one
  // singleton across both calls would break whichever call reads it second.
  const fetchMock = vi.fn().mockImplementation((url: unknown) => {
    if (requestPath(url).startsWith('/api/search')) return Promise.resolve(jsonResponse(200, { degraded: false, results: [] }))
    return Promise.resolve(jsonResponse(200, { ...row, status: 'approved' }))
  })
  vi.stubGlobal('fetch', fetchMock)
  const onDone = vi.fn()
  renderPanel(row, onDone)
  const name = screen.getByLabelText('Name')
  await userEvent.clear(name)
  await userEvent.type(name, 'Repro Alpha')
  await userEvent.click(screen.getByRole('button', { name: 'Approve as new product' }))
  const verdictCall = fetchMock.mock.calls.find(([u]) => requestPath(u) === '/api/admin/submissions/s1/verdict')
  expect(verdictCall).toBeDefined()
  expect(await putBody(verdictCall?.[0])).toEqual({
    action: 'approve_new',
    product: { type: 'game', name: 'Repro Alpha', platform_name: 'snes', region: 'pal', edition: 'glow cart' },
  })
  // Argument-free: only the raced-409 path below hands anything up.
  expect(onDone).toHaveBeenCalledWith()
})

// Type options used to be raw, untranslated text (a straight
// "game"/"console"/"accessory" <option> literal) - this pins that they
// now render through itemTypeWireLabels, so a ja reader sees ja text
// rather than the English wire value. Options are found by their
// locale-invariant value (not the label text, which is itself
// translated under ja) so the query does not depend on the very
// translation being pinned.
it('renders the Type options through the translated wire label under ja', () => {
  vi.stubGlobal('fetch', vi.fn().mockResolvedValue(jsonResponse(200, { degraded: false, results: [] })))
  i18n.load('ja', jaMessages)
  i18n.activate('ja')
  renderPanel(row)
  // Found by the option's value attribute (locale-invariant) rather
  // than getByDisplayValue, which matches a select's VISIBLE option
  // text - itself translated under ja, so it cannot locate the node.
  const select = document.querySelector('option[value="game"]')!.parentElement!
  const texts = Array.from(select.querySelectorAll('option')).map((o) => o.textContent)
  expect(texts).toEqual(['ゲーム', 'ゲーム機', '周辺機器'])
})

it('a catalog platform pick shows the confirmed state (not a blank field) and mints the canonical name', async () => {
  // No platform_name prefill: the picker mounts straight into its
  // catalog-search branch (the prefilled-free-text path is covered by
  // the test above and must stay untouched).
  const noPlatform: AdminSubmission = {
    id: 's2', entry_id: 'e2', user_id: 'u1', status: 'pending',
    display_name: 'Chrono Trigger', item_type: 'game',
    region: 'pal', created_at: '2026-07-17T00:00:00Z', updated_at: '2026-07-17T00:00:00Z',
  }
  const fetchMock = vi.fn().mockImplementation((url: unknown) => {
    if (requestPath(url).startsWith('/api/search')) return Promise.resolve(jsonResponse(200, { degraded: false, results: [] }))
    return Promise.resolve(jsonResponse(200, { ...noPlatform, status: 'approved' }))
  })
  vi.stubGlobal('fetch', fetchMock)
  const onDone = vi.fn()
  const platforms: Platform[] = [{ igdb_id: 19, name: 'Super Nintendo Entertainment System', aliases: ['snes', 'super nintendo'] }]
  renderPanel(noPlatform, onDone, platforms)

  await userEvent.type(screen.getByLabelText(/^platform$/i), 'snes')
  await userEvent.click(await screen.findByRole('button', { name: 'Super Nintendo Entertainment System' }))

  // The regression this guards: the picked name used to vanish into
  // panel state with the visible field left blank. Confirmed state
  // means the field is gone, replaced by the canonical name + Change.
  expect(screen.queryByLabelText(/^platform$/i)).not.toBeInTheDocument()
  expect(screen.getByText('Super Nintendo Entertainment System')).toBeInTheDocument()
  expect(screen.getByRole('button', { name: 'Change platform' })).toBeInTheDocument()

  await userEvent.click(screen.getByRole('button', { name: 'Approve as new product' }))
  const verdictCall = fetchMock.mock.calls.find(([u]) => requestPath(u) === '/api/admin/submissions/s2/verdict')
  expect(verdictCall).toBeDefined()
  // Name-only by design (community facts carry platform_name, never a
  // platform id) - toEqual on the whole body proves no id field rode along.
  expect(await putBody(verdictCall?.[0])).toEqual({
    action: 'approve_new',
    product: { type: 'game', name: 'Chrono Trigger', platform_name: 'Super Nintendo Entertainment System', region: 'pal' },
  })
  expect(onDone).toHaveBeenCalled()
})

it('renders the region as a RegionPicker select prefilled with the submission region, and mints the edited region', async () => {
  const fetchMock = vi.fn().mockImplementation((url: unknown) => {
    if (requestPath(url).startsWith('/api/search')) return Promise.resolve(jsonResponse(200, { degraded: false, results: [] }))
    return Promise.resolve(jsonResponse(200, { ...row, status: 'approved' }))
  })
  vi.stubGlobal('fetch', fetchMock)
  const onDone = vi.fn()
  renderPanel(row, onDone)
  const regionField = screen.getByLabelText('Region')
  expect(regionField).toHaveValue('pal')
  // The known-value branch: a select, with the free-text escape hatch
  // alongside it rather than the text-mode "pick a known region" link.
  expect(screen.getByRole('button', { name: "My region isn't listed" })).toBeInTheDocument()
  await userEvent.selectOptions(regionField, 'ntsc_u')
  await userEvent.click(screen.getByRole('button', { name: 'Approve as new product' }))
  const verdictCall = fetchMock.mock.calls.find(([u]) => requestPath(u) === '/api/admin/submissions/s1/verdict')
  expect(await putBody(verdictCall?.[0])).toEqual({
    action: 'approve_new',
    product: { type: 'game', name: 'repro alpha', platform_name: 'snes', region: 'ntsc_u', edition: 'glow cart' },
  })
})

it('opens region in free-text mode for a stored value outside the known set, and mints the edited free-text region', async () => {
  const openWorldRow: AdminSubmission = { ...row, region: 'Korea' }
  const fetchMock = vi.fn().mockImplementation((url: unknown) => {
    if (requestPath(url).startsWith('/api/search')) return Promise.resolve(jsonResponse(200, { degraded: false, results: [] }))
    return Promise.resolve(jsonResponse(200, { ...openWorldRow, status: 'approved' }))
  })
  vi.stubGlobal('fetch', fetchMock)
  const onDone = vi.fn()
  renderPanel(openWorldRow, onDone)
  const regionField = screen.getByLabelText('Region')
  expect(regionField).toHaveValue('Korea')
  expect(screen.getByRole('button', { name: 'Pick a known region instead' })).toBeInTheDocument()
  await userEvent.clear(regionField)
  await userEvent.type(regionField, 'South Korea')
  await userEvent.click(screen.getByRole('button', { name: 'Approve as new product' }))
  const verdictCall = fetchMock.mock.calls.find(([u]) => requestPath(u) === '/api/admin/submissions/s1/verdict')
  expect(await putBody(verdictCall?.[0])).toEqual({
    action: 'approve_new',
    product: { type: 'game', name: 'repro alpha', platform_name: 'snes', region: 'South Korea', edition: 'glow cart' },
  })
})

it('prefills the submitter credits and mints the edited lists', async () => {
  const credited: AdminSubmission = { ...row, developers: ['Garage Team'] }
  const fetchMock = vi.fn().mockImplementation((url: unknown) => {
    if (requestPath(url).startsWith('/api/search')) return Promise.resolve(jsonResponse(200, { degraded: false, results: [] }))
    return Promise.resolve(jsonResponse(200, { ...credited, status: 'approved' }))
  })
  vi.stubGlobal('fetch', fetchMock)
  renderPanel(credited, vi.fn())
  expect(screen.getByLabelText('Developers 1')).toHaveValue('Garage Team')
  await userEvent.click(screen.getByRole('button', { name: 'Add publisher' }))
  await userEvent.type(screen.getByLabelText('Publishers 1'), '  Repro House  ')
  await userEvent.click(screen.getByRole('button', { name: 'Approve as new product' }))
  const verdictCall = fetchMock.mock.calls.find(([u]) => requestPath(u) === '/api/admin/submissions/s1/verdict')
  expect(await putBody(verdictCall?.[0])).toEqual({
    action: 'approve_new',
    product: {
      type: 'game', name: 'repro alpha', platform_name: 'snes', region: 'pal', edition: 'glow cart',
      developers: ['Garage Team'], publishers: ['Repro House'],
    },
  })
})

it('reject requires and sends the reason', async () => {
  const fetchMock = vi.fn().mockImplementation((url: unknown) => {
    if (requestPath(url).startsWith('/api/search')) return Promise.resolve(jsonResponse(200, { degraded: false, results: [] }))
    return Promise.resolve(jsonResponse(200, { ...row, status: 'rejected' }))
  })
  vi.stubGlobal('fetch', fetchMock)
  const onDone = vi.fn()
  renderPanel(row, onDone)
  const rejectButton = screen.getByRole('button', { name: 'Reject' })
  expect(rejectButton).toBeDisabled()
  await userEvent.type(screen.getByLabelText('Rejection reason'), 'Duplicate of an existing product')
  expect(rejectButton).toBeEnabled()
  await userEvent.click(rejectButton)
  const verdictCall = fetchMock.mock.calls.find(([u]) => requestPath(u) === '/api/admin/submissions/s1/verdict')
  expect(verdictCall).toBeDefined()
  expect(await putBody(verdictCall?.[0])).toEqual({
    action: 'reject',
    reason: 'Duplicate of an existing product',
  })
  expect(onDone).toHaveBeenCalled()
})

it('adopt by id sends approve_existing', async () => {
  const fetchMock = vi.fn().mockImplementation((url: unknown) => {
    if (requestPath(url).startsWith('/api/search')) return Promise.resolve(jsonResponse(200, { degraded: false, results: [] }))
    return Promise.resolve(jsonResponse(200, { ...row, status: 'approved' }))
  })
  vi.stubGlobal('fetch', fetchMock)
  const onDone = vi.fn()
  renderPanel(row, onDone)
  await userEvent.click(screen.getByRole('button', { name: 'Adopt existing product' }))
  await userEvent.type(screen.getByLabelText('Product id'), '3fa85f64-5717-4562-b3fc-2c963f66afa6')
  await userEvent.click(screen.getByRole('button', { name: 'Adopt by id' }))
  const post = fetchMock.mock.calls.find(([input]) => (input as Request).method === 'POST')
  expect(requestPath(post?.[0])).toBe('/api/admin/submissions/s1/verdict')
  expect(await putBody(post?.[0])).toEqual({
    action: 'approve_existing',
    product_id: '3fa85f64-5717-4562-b3fc-2c963f66afa6',
  })
  expect(onDone).toHaveBeenCalled()
})

it('adopt via a community pick adopts directly, without resolving', async () => {
  const communityProductId = '11111111-2222-4333-8444-555555555555'
  const fetchMock = vi.fn().mockImplementation((url: unknown) => {
    if (requestPath(url).startsWith('/api/search'))
      return Promise.resolve(jsonResponse(200, {
        degraded: false,
        results: [
          {
            type: 'game', name: 'Repro Alpha', origin: 'community',
            product_id: communityProductId, item_type: 'game', platform_name: 'SNES',
          },
        ],
      }))
    return Promise.resolve(jsonResponse(200, { ...row, status: 'approved' }))
  })
  vi.stubGlobal('fetch', fetchMock)
  const onDone = vi.fn()
  renderPanel(row, onDone)
  await userEvent.click(screen.getByRole('button', { name: 'Adopt existing product' }))
  await userEvent.click(await screen.findByRole('button', { name: 'Search' }))
  await userEvent.click(await screen.findByRole('button', { name: 'Repro Alpha on SNES' }))
  const post = fetchMock.mock.calls.find(([input]) => (input as Request).method === 'POST')
  expect(requestPath(post?.[0])).toBe('/api/admin/submissions/s1/verdict')
  expect(await putBody(post?.[0])).toEqual({
    action: 'approve_existing',
    product_id: communityProductId,
  })
  expect(fetchMock.mock.calls.some(([u]) => requestPath(u) === '/api/products/resolve')).toBe(false)
  expect(onDone).toHaveBeenCalled()
})

it('adopt via a provider pick resolves first, then adopts the resolved product', async () => {
  const resolvedId = '9f9f9f9f-0000-0000-0000-000000000001'
  const fetchMock = vi.fn().mockImplementation((url: unknown) => {
    if (requestPath(url).startsWith('/api/search'))
      return Promise.resolve(jsonResponse(200, {
        degraded: false,
        results: [{ type: 'game', name: 'Chrono Trigger', igdb_game_id: 1011,
          platforms: [{ igdb_platform_id: 19, name: 'SNES' }] }],
      }))
    if (requestPath(url) === '/api/products/resolve')
      return Promise.resolve(jsonResponse(200, {
        id: resolvedId, type: 'game', name: 'Chrono Trigger', igdb: { game_id: 1011 },
        created_at: 'x', updated_at: 'x',
      }))
    return Promise.resolve(jsonResponse(200, { ...row, status: 'approved' }))
  })
  vi.stubGlobal('fetch', fetchMock)
  const onDone = vi.fn()
  renderPanel(row, onDone)
  await userEvent.click(screen.getByRole('button', { name: 'Adopt existing product' }))
  await userEvent.click(await screen.findByRole('button', { name: 'Search' }))
  await userEvent.click(await screen.findByRole('button', { name: 'Chrono Trigger on SNES' }))
  const resolveCall = fetchMock.mock.calls.find(([u]) => requestPath(u) === '/api/products/resolve')
  expect(resolveCall).toBeDefined()
  expect(await putBody(resolveCall?.[0])).toEqual({
    type: 'game', igdb_game_id: 1011, platform_igdb_id: 19,
  })
  const verdictCall = fetchMock.mock.calls.find(([u]) => requestPath(u) === '/api/admin/submissions/s1/verdict')
  expect(await putBody(verdictCall?.[0])).toEqual({
    action: 'approve_existing', product_id: resolvedId,
  })
  expect(onDone).toHaveBeenCalled()
})

it('opens the adopt picker on the kind matching the submission: Hardware for hardware, Games for a game', async () => {
  const fetchMock = vi.fn().mockImplementation((url: unknown) => {
    if (requestPath(url).startsWith('/api/search')) return Promise.resolve(jsonResponse(200, { degraded: false, results: [] }))
    return Promise.resolve(jsonResponse(200, row))
  })
  vi.stubGlobal('fetch', fetchMock)

  renderPanel({ ...row, item_type: 'console' })
  await userEvent.click(screen.getByRole('button', { name: 'Adopt existing product' }))
  expect(screen.getByRole('radio', { name: 'Hardware' })).toBeChecked()
  expect(screen.getByRole('radio', { name: 'Games' })).not.toBeChecked()
  cleanup()

  renderPanel(row) // row.item_type is 'game'
  await userEvent.click(screen.getByRole('button', { name: 'Adopt existing product' }))
  expect(screen.getByRole('radio', { name: 'Games' })).toBeChecked()
})

it('renders submission_resolved inline and refetches', async () => {
  const fetchMock = vi.fn().mockImplementation((url: unknown) => {
    if (requestPath(url).startsWith('/api/search')) return Promise.resolve(jsonResponse(200, { degraded: false, results: [] }))
    return Promise.resolve(problemResponse(409, 'submission_resolved', 'Another admin already handled this submission.'))
  })
  vi.stubGlobal('fetch', fetchMock)
  const onDone = vi.fn()
  renderPanel(row, onDone)
  await userEvent.click(screen.getByRole('button', { name: 'Approve as new product' }))
  expect(await screen.findByRole('alert')).toHaveTextContent('Another admin already resolved this submission.')
  // The error itself rides up, not a phrased message: the queue holds it
  // and phrases it at render time, so it survives a locale switch.
  const [carried] = onDone.mock.calls[0] as [ApiError]
  expect(carried).toBeInstanceOf(ApiError)
  expect(carried.code).toBe('submission_resolved')
})

it('prefills the cover, previews it, and sends it in the approve_new mint', async () => {
  const row: AdminSubmission = {
    id: 's1', entry_id: 'e1', user_id: 'u1', status: 'pending',
    display_name: 'Repro Alpha', item_type: 'game', platform_name: 'SNES',
    region: 'pal', cover_url: 'https://img.example/sub.jpg',
    created_at: '2026-07-19T00:00:00Z', updated_at: '2026-07-19T00:00:00Z',
  }
  const fetchMock = vi.fn().mockImplementation((url: unknown) => {
    if (requestPath(url).startsWith('/api/search')) return Promise.resolve(jsonResponse(200, { degraded: false, results: [] }))
    return Promise.resolve(jsonResponse(200, { id: 's1', entry_id: 'e1', status: 'approved', created_at: 'x', updated_at: 'x', product_id: 'p1' }))
  })
  vi.stubGlobal('fetch', fetchMock)
  renderPanel(row) // the file's existing render helper + query seeding
  const cover = screen.getByLabelText(/cover image link/i)
  expect(cover).toHaveValue('https://img.example/sub.jpg')
  expect(screen.getByRole('img', { name: /cover preview/i })).toHaveAttribute('src', 'https://img.example/sub.jpg')
  await userEvent.click(screen.getByRole('button', { name: 'Approve as new product' }))
  const verdictCall = fetchMock.mock.calls.find(([u]) => requestPath(u) === '/api/admin/submissions/s1/verdict')
  const body = await putBody<{ product: { cover_url?: string } }>(verdictCall?.[0])
  expect(body.product.cover_url).toBe('https://img.example/sub.jpg')
})

it('shows potential duplicates for the proposal name, including a community-tagged row', async () => {
  const fetchMock = vi.fn().mockImplementation((url: unknown) => {
    if (requestPath(url).startsWith('/api/search'))
      return Promise.resolve(jsonResponse(200, {
        degraded: false,
        results: [
          {
            type: 'game', name: 'Repro Alpha', igdb_game_id: 42,
            first_release_date: '1994-05-01', console_name: 'Super Nintendo',
          },
          {
            type: 'game', name: 'Repro Alpha', origin: 'community',
            product_id: 'dupe-1', item_type: 'game', platform_name: 'SNES',
          },
        ],
      }))
    return Promise.resolve(jsonResponse(200, row))
  })
  vi.stubGlobal('fetch', fetchMock)
  renderPanel(row)
  const section = (await screen.findByText('Potential duplicates')).closest('div')!
  expect(within(section).getAllByText('Repro Alpha')).toHaveLength(2)
  expect(within(section).getByText('community')).toBeInTheDocument()
  expect(within(section).getByText('1994')).toBeInTheDocument()
  expect(within(section).getByText('Super Nintendo')).toBeInTheDocument()
  expect(within(section).getByText('SNES')).toBeInTheDocument()
  // Searched on the submission's own proposal (display_name), type game.
  expect(requestPath(fetchMock.mock.calls[0][0])).toContain('type=game&q=repro+alpha')
})

it('flags an exact-match duplicate row by name+platform, not a differently-named or differently-platformed row', async () => {
  const fetchMock = vi.fn().mockImplementation((url: unknown) => {
    if (requestPath(url).startsWith('/api/search'))
      return Promise.resolve(jsonResponse(200, {
        degraded: false,
        results: [
          // Same name (case-insensitive), same platform (case-insensitive): exact.
          { type: 'game', name: 'Repro Alpha', origin: 'community', product_id: 'dupe-exact', item_type: 'game', platform_name: 'SNES' },
          // Different name: not exact, even with the same platform.
          { type: 'game', name: 'Chrono Trigger', origin: 'community', product_id: 'dupe-diff-name', item_type: 'game', platform_name: 'SNES' },
          // Same name, different platform (both sides present): not exact.
          { type: 'game', name: 'Repro Alpha', origin: 'community', product_id: 'dupe-diff-platform', item_type: 'game', platform_name: 'Genesis' },
          // Same name, row carries no platform at all (no console_name, no
          // platform_name): the name-only fallback still counts as exact,
          // since a missing platform is a data gap, not a mismatch signal.
          { type: 'game', name: 'Repro Alpha', origin: 'community', product_id: 'dupe-no-platform', item_type: 'game' },
        ],
      }))
    return Promise.resolve(jsonResponse(200, row))
  })
  vi.stubGlobal('fetch', fetchMock)
  renderPanel(row)
  const section = (await screen.findByText('Potential duplicates')).closest('div')!
  const items = within(section).getAllByRole('listitem')
  const exactRow = items.find((li) => li.textContent?.includes('SNES') && li.textContent?.includes('Repro Alpha'))!
  const diffNameRow = items.find((li) => li.textContent?.includes('Chrono Trigger'))!
  const diffPlatformRow = items.find((li) => li.textContent?.includes('Genesis'))!
  const noPlatformRow = items.find(
    (li) => li.textContent?.includes('Repro Alpha') && !li.textContent?.includes('SNES') && !li.textContent?.includes('Genesis'),
  )!
  expect(within(exactRow).getByText('exact match')).toBeInTheDocument()
  expect(within(diffNameRow).queryByText('exact match')).not.toBeInTheDocument()
  expect(within(diffPlatformRow).queryByText('exact match')).not.toBeInTheDocument()
  expect(within(noPlatformRow).getByText('exact match')).toBeInTheDocument()
})

it('Use as existing on a community duplicate row adopts directly, without resolving, and provider rows get no such button', async () => {
  const communityProductId = '11111111-2222-4333-8444-555555555555'
  const fetchMock = vi.fn().mockImplementation((url: unknown) => {
    if (requestPath(url).startsWith('/api/search'))
      return Promise.resolve(jsonResponse(200, {
        degraded: false,
        results: [
          {
            type: 'game', name: 'Repro Alpha', igdb_game_id: 42, console_name: 'Super Nintendo',
          },
          {
            type: 'game', name: 'Repro Alpha', origin: 'community',
            product_id: communityProductId, item_type: 'game', platform_name: 'SNES',
          },
        ],
      }))
    return Promise.resolve(jsonResponse(200, { ...row, status: 'approved' }))
  })
  vi.stubGlobal('fetch', fetchMock)
  const onDone = vi.fn()
  renderPanel(row, onDone)
  const section = (await screen.findByText('Potential duplicates')).closest('div')!
  // Exactly one: the community row gets the button, the provider row does not.
  expect(within(section).getAllByRole('button', { name: 'Use as existing' })).toHaveLength(1)
  await userEvent.click(within(section).getByRole('button', { name: 'Use as existing' }))
  const post = fetchMock.mock.calls.find(([input]) => (input as Request).method === 'POST')
  expect(requestPath(post?.[0])).toBe('/api/admin/submissions/s1/verdict')
  expect(await putBody(post?.[0])).toEqual({
    action: 'approve_existing',
    product_id: communityProductId,
  })
  expect(fetchMock.mock.calls.some(([u]) => requestPath(u) === '/api/products/resolve')).toBe(false)
  expect(onDone).toHaveBeenCalled()
})

it('shows None found when no duplicates match the proposal name', async () => {
  const fetchMock = vi.fn().mockImplementation((url: unknown) => {
    if (requestPath(url).startsWith('/api/search')) return Promise.resolve(jsonResponse(200, { degraded: false, results: [] }))
    return Promise.resolve(jsonResponse(200, row))
  })
  vi.stubGlobal('fetch', fetchMock)
  renderPanel(row)
  expect(await screen.findByText('None found.')).toBeInTheDocument()
})

it('maps a console/accessory submission to a hardware duplicates search', async () => {
  const fetchMock = vi.fn().mockImplementation((url: unknown) => {
    if (requestPath(url).startsWith('/api/search')) return Promise.resolve(jsonResponse(200, { degraded: false, results: [] }))
    return Promise.resolve(jsonResponse(200, row))
  })
  vi.stubGlobal('fetch', fetchMock)
  renderPanel({ ...row, item_type: 'console' })
  await screen.findByText('None found.')
  expect(requestPath(fetchMock.mock.calls[0][0])).toContain('type=hardware')
})
