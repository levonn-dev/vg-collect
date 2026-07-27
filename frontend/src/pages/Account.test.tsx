import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { cleanup, render, screen, waitFor, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { MemoryRouter, Route, Routes, useLocation } from 'react-router'
import Account from './Account'

const jsonResponse = (status: number, body: unknown) =>
  new Response(JSON.stringify(body), {
    status,
    headers: { 'Content-Type': 'application/json' },
  })

const me = {
  id: 'u1', email: 'alice@example.com', handle: 'Alice', roles: ['user'], profile_visibility: 'private',
  landing_page: 'feed',
}
const identities = {
  identities: [
    { id: 'i1', provider: 'dev', email: 'alice@example.com', created_at: '2026-01-01T00:00:00Z' },
    { id: 'i2', provider: 'dev', email: 'bob@example.com', created_at: '2026-02-01T00:00:00Z' },
  ],
}
const providers = { providers: ['google', 'dev'] }

// route fetches by URL so each test can vary one answer. Overrides only
// apply to non-GET calls (unlink/delete), so a shared URL prefix cannot
// hijack the GET fixtures below; those vary per test via `data`.
function stubFetch(
  overrides: Record<string, Response> = {},
  data: { me?: typeof me; identities?: typeof identities; providers?: typeof providers } = {},
) {
  const fetchMock = vi.fn((input: string, init?: RequestInit) => {
    const url = String(input)
    for (const [prefix, res] of Object.entries(overrides)) {
      if (url.startsWith(prefix) && (init?.method ?? 'GET') !== 'GET') return Promise.resolve(res.clone())
    }
    if (url === '/api/me') return Promise.resolve(jsonResponse(200, data.me ?? me))
    if (url === '/api/me/identities') return Promise.resolve(jsonResponse(200, data.identities ?? identities))
    if (url === '/api/auth/providers') return Promise.resolve(jsonResponse(200, data.providers ?? providers))
    return Promise.resolve(jsonResponse(200, {}))
  })
  vi.stubGlobal('fetch', fetchMock)
  return fetchMock
}

// Renders wherever navigate('/login?...') lands as plain text so tests
// can assert the exact post-navigation path and query string.
function LocationProbe() {
  const location = useLocation()
  return <div>{location.pathname + location.search}</div>
}

function renderAccount(path = '/account') {
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } })
  return render(
    <QueryClientProvider client={qc}>
      <MemoryRouter initialEntries={[path]}>
        <Routes>
          <Route path="/account" element={<Account />} />
          <Route path="/login" element={<LocationProbe />} />
        </Routes>
      </MemoryRouter>
    </QueryClientProvider>,
  )
}

afterEach(() => vi.unstubAllGlobals())

it('renders profile fields seeded from /api/me and saves via PATCH', async () => {
  const fetchMock = stubFetch()
  renderAccount()
  const input = await screen.findByLabelText('Handle')
  expect(input).toHaveValue('Alice')
  await userEvent.clear(input)
  await userEvent.type(input, 'Alicia')
  await userEvent.click(screen.getByRole('button', { name: 'Save' }))
  expect(await screen.findByRole('status')).toHaveTextContent('Saved.')
  expect(fetchMock).toHaveBeenCalledWith('/api/me', expect.objectContaining({
    method: 'PATCH',
    body: JSON.stringify({
      handle: 'Alicia', avatar_url: '', profile_visibility: 'private', landing_page: 'feed',
    }),
  }))
})

it('carries client-side handle validation matching the server rules', async () => {
  stubFetch()
  renderAccount()
  const input = await screen.findByLabelText('Handle')
  expect(input).toHaveAttribute('minLength', '2')
  expect(input).toHaveAttribute('pattern', '[a-zA-Z0-9](?:[a-zA-Z0-9_]{0,28}[a-zA-Z0-9])?')
  expect(input).toHaveAttribute('title', '2-30 characters, letters/digits, underscores inside only')
})

it('submits the selected profile visibility alongside the rest of the form', async () => {
  const fetchMock = stubFetch()
  renderAccount()
  await userEvent.click(
    await screen.findByRole('radio', { name: 'Listed - appears in Explore and search' }),
  )
  await userEvent.click(screen.getByRole('button', { name: 'Save' }))
  expect(await screen.findByRole('status')).toHaveTextContent('Saved.')
  expect(fetchMock).toHaveBeenCalledWith('/api/me', expect.objectContaining({
    method: 'PATCH',
    body: JSON.stringify({
      handle: 'Alice', avatar_url: '', profile_visibility: 'listed', landing_page: 'feed',
    }),
  }))
})

it('renders the default-page fieldset seeded from me.landing_page', async () => {
  stubFetch({}, { me: { ...me, landing_page: 'explore' } })
  renderAccount()
  expect(await screen.findByRole('radio', { name: 'Explore' })).toBeChecked()
  expect(screen.getByRole('radio', { name: 'Feed' })).not.toBeChecked()
  expect(screen.getByRole('radio', { name: 'Collection' })).not.toBeChecked()
  expect(screen.getByText('Where the app opens after you sign in.')).toBeInTheDocument()
})

it('submits the selected default page alongside the rest of the form', async () => {
  const fetchMock = stubFetch()
  renderAccount()
  await userEvent.click(await screen.findByRole('radio', { name: 'Collection' }))
  await userEvent.click(screen.getByRole('button', { name: 'Save' }))
  expect(await screen.findByRole('status')).toHaveTextContent('Saved.')
  expect(fetchMock).toHaveBeenCalledWith('/api/me', expect.objectContaining({
    method: 'PATCH',
    body: JSON.stringify({
      handle: 'Alice', avatar_url: '', profile_visibility: 'private', landing_page: 'collection',
    }),
  }))
})

it('shows a specific message when the handle is taken', async () => {
  const problem = jsonResponse(409, {
    type: 'about:blank', title: 'Conflict', status: 409, code: 'handle_taken',
  })
  stubFetch({ '/api/me': problem })
  renderAccount()
  await userEvent.click(await screen.findByRole('button', { name: 'Save' }))
  expect(await screen.findByRole('alert')).toHaveTextContent('That handle is taken.')
})

it('shows a specific message when the handle cooldown blocks the change', async () => {
  const problem = jsonResponse(429, {
    type: 'about:blank', title: 'Too Many Requests', status: 429, code: 'handle_cooldown',
  })
  stubFetch({ '/api/me': problem })
  renderAccount()
  await userEvent.click(await screen.findByRole('button', { name: 'Save' }))
  expect(await screen.findByRole('alert'))
    .toHaveTextContent('Handle changed too recently - try again later.')
})

it('hides the copy-link button while the profile is private', async () => {
  stubFetch()
  renderAccount()
  await screen.findByLabelText('Handle')
  expect(screen.queryByRole('button', { name: 'Copy profile link' })).not.toBeInTheDocument()
})

it('copies the profile link once visibility is not private', async () => {
  const writeText = vi.fn().mockResolvedValue(undefined)
  Object.defineProperty(navigator, 'clipboard', { value: { writeText }, configurable: true })
  stubFetch({}, { me: { ...me, profile_visibility: 'listed' } })
  renderAccount()
  await userEvent.click(await screen.findByRole('button', { name: 'Copy profile link' }))
  expect(writeText).toHaveBeenCalledWith(`${window.location.origin}/u/Alice`)
})

it('lists linked logins with provider and email', async () => {
  stubFetch()
  renderAccount()
  const list = await screen.findByRole('list')
  expect(within(list).getByText(/alice@example\.com/)).toBeInTheDocument()
  expect(within(list).getByText(/bob@example\.com/)).toBeInTheDocument()
  expect(within(list).getAllByText('dev')).toHaveLength(2)
})

it('disables Unlink on the last remaining login', async () => {
  stubFetch({}, { identities: { identities: [identities.identities[0]] } })
  renderAccount()
  const button = await screen.findByRole('button', { name: 'Unlink' })
  expect(button).toBeDisabled()
  expect(button).toHaveAttribute('title', 'Your account needs at least one login')
})

it('unlinks after confirmation', async () => {
  vi.stubGlobal('confirm', vi.fn(() => true))
  const fetchMock = stubFetch({ '/api/me/identities/': new Response(null, { status: 204 }) })
  renderAccount()
  const unlinkButtons = await screen.findAllByRole('button', { name: 'Unlink' })
  await userEvent.click(unlinkButtons[1])
  await waitFor(() => {
    expect(fetchMock).toHaveBeenCalledWith(
      '/api/me/identities/i2',
      expect.objectContaining({ method: 'DELETE' }),
    )
  })
})

it('renders link buttons as navigations to /api/auth/link', async () => {
  stubFetch()
  renderAccount()
  expect(await screen.findByRole('link', { name: 'Link Google' }))
    .toHaveAttribute('href', '/api/auth/link?provider=google')
  for (const user of ['alice', 'bob', 'admin']) {
    expect(screen.getByRole('link', { name: user }))
      .toHaveAttribute('href', `/api/auth/link?provider=dev&user=${user}`)
  }
})

it('shows the linked and conflict notices from query params', async () => {
  stubFetch()
  renderAccount('/account?linked=dev')
  expect(await screen.findByRole('status')).toHaveTextContent(/login linked/i)
  cleanup()

  stubFetch()
  renderAccount('/account?link_error=conflict')
  expect(await screen.findByRole('alert')).toHaveTextContent(/already belongs to another account/i)
})

it('deletes the account after confirmation and navigates to login', async () => {
  vi.stubGlobal('confirm', vi.fn(() => true))
  const fetchMock = stubFetch({ '/api/me': new Response(null, { status: 204 }) })
  renderAccount()
  await userEvent.click(await screen.findByRole('button', { name: 'Delete account' }))
  expect(await screen.findByText('/login?deleted=1')).toBeInTheDocument()
  expect(fetchMock).toHaveBeenCalledWith('/api/me', expect.objectContaining({ method: 'DELETE' }))
})
