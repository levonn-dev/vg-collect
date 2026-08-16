import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { MemoryRouter, Route, Routes, useLocation } from 'react-router'
import { jsonResponse, meFixture } from '../test/fixtures'
import { renderWithI18n } from '../test/i18n'
import Login from './Login'

function LocationProbe() {
  const location = useLocation()
  return <output aria-label="location">{location.pathname}</output>
}

// Each test stubs fetch for the providers call before rendering; this
// wrapper layers the session probe's /api/me on top (signed out by
// default) so Login's own query never swallows the providers response.
function renderLogin(path = '/login', { authed = false } = {}) {
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } })
  const providersFetch = window.fetch
  vi.stubGlobal('fetch', vi.fn((input: RequestInfo | URL, init?: RequestInit) =>
    (input instanceof Request ? input.url : String(input)) === '/api/me'
      ? Promise.resolve(authed ? jsonResponse(200, meFixture()) : jsonResponse(401, { error: 'unauthorized' }))
      : providersFetch(input, init)))
  return renderWithI18n(
    <QueryClientProvider client={qc}>
      <MemoryRouter initialEntries={[path]}>
        <Routes>
          <Route path="/login" element={<Login />} />
          <Route path="*" element={<LocationProbe />} />
        </Routes>
      </MemoryRouter>
    </QueryClientProvider>,
  )
}

// jsdom does not implement real cross-page navigation; clicking a
// login link would otherwise log a noisy "not implemented" warning
// once its default action runs. The stash handler under test still
// fires - only the browser's own follow-the-link behavior is skipped.
function clickWithoutNavigating(link: HTMLElement) {
  link.addEventListener('click', (e) => e.preventDefault())
  return userEvent.click(link)
}

afterEach(() => {
  vi.unstubAllGlobals()
  sessionStorage.clear()
})

it('renders one button per enabled provider, dev as fixture quick-logins', async () => {
  vi.stubGlobal('fetch', vi.fn().mockResolvedValue(
    jsonResponse(200, { providers: ['google', 'dev'] })))
  renderLogin()
  expect(await screen.findByRole('link', { name: 'Continue with Google' }))
    .toHaveAttribute('href', '/api/auth/login?provider=google')
  for (const user of ['alice', 'bob', 'admin']) {
    expect(screen.getByRole('link', { name: user }))
      .toHaveAttribute('href', `/api/auth/login?provider=dev&user=${user}`)
  }
  expect(screen.queryByRole('link', { name: 'Continue with Twitch' })).toBeNull()
})

it('hides dev fixtures when the dev provider is off', async () => {
  vi.stubGlobal('fetch', vi.fn().mockResolvedValue(
    jsonResponse(200, { providers: ['google', 'twitch'] })))
  renderLogin()
  expect(await screen.findByRole('link', { name: 'Continue with Twitch' })).toBeInTheDocument()
  expect(screen.queryByRole('link', { name: 'alice' })).toBeNull()
})

it('shows the error banner from the redirect code', async () => {
  vi.stubGlobal('fetch', vi.fn().mockResolvedValue(jsonResponse(200, { providers: [] })))
  renderLogin('/login?error=email_unverified')
  expect(await screen.findByRole('alert')).toHaveTextContent(/verified email/i)
})

it('reports when providers cannot be loaded', async () => {
  vi.stubGlobal('fetch', vi.fn().mockResolvedValue(new Response('down', { status: 502 })))
  renderLogin()
  expect(await screen.findByRole('alert')).toHaveTextContent(/unavailable/i)
})

it('labels an unknown provider with a generic fallback', async () => {
  vi.stubGlobal('fetch', vi.fn().mockResolvedValue(
    jsonResponse(200, { providers: ['discord'] })))
  renderLogin()
  expect(await screen.findByRole('link', { name: 'Continue with discord' }))
    .toHaveAttribute('href', '/api/auth/login?provider=discord')
})

it('shows the account-deleted notice', async () => {
  vi.stubGlobal('fetch', vi.fn().mockResolvedValue(jsonResponse(200, { providers: [] })))
  renderLogin('/login?deleted=1')
  expect(await screen.findByRole('status')).toHaveTextContent(/account.*deleted/i)
})

it('stashes an internal next path before a provider link navigates away', async () => {
  vi.stubGlobal('fetch', vi.fn().mockResolvedValue(jsonResponse(200, { providers: ['google'] })))
  renderLogin('/login?next=%2Faccount')
  const link = await screen.findByRole('link', { name: 'Continue with Google' })
  await clickWithoutNavigating(link)
  expect(sessionStorage.getItem('vg_next')).toBe('/account')
})

it('stashes next for dev fixture links the same way', async () => {
  vi.stubGlobal('fetch', vi.fn().mockResolvedValue(jsonResponse(200, { providers: ['dev'] })))
  renderLogin('/login?next=%2Faccount')
  const link = await screen.findByRole('link', { name: 'alice' })
  await clickWithoutNavigating(link)
  expect(sessionStorage.getItem('vg_next')).toBe('/account')
})

it('rejects a protocol-relative next value (safeNext)', async () => {
  vi.stubGlobal('fetch', vi.fn().mockResolvedValue(jsonResponse(200, { providers: ['google'] })))
  renderLogin(`/login?next=${encodeURIComponent('//evil.example')}`)
  const link = await screen.findByRole('link', { name: 'Continue with Google' })
  await clickWithoutNavigating(link)
  expect(sessionStorage.getItem('vg_next')).toBeNull()
})

it('rejects an external next value (safeNext)', async () => {
  vi.stubGlobal('fetch', vi.fn().mockResolvedValue(jsonResponse(200, { providers: ['google'] })))
  renderLogin(`/login?next=${encodeURIComponent('https://evil.example/x')}`)
  const link = await screen.findByRole('link', { name: 'Continue with Google' })
  await clickWithoutNavigating(link)
  expect(sessionStorage.getItem('vg_next')).toBeNull()
})

it('redirects an authenticated session to home', async () => {
  vi.stubGlobal('fetch', vi.fn().mockResolvedValue(jsonResponse(200, { providers: ['google'] })))
  renderLogin('/login', { authed: true })
  expect(await screen.findByLabelText('location')).toHaveTextContent('/')
})

it('redirects an authenticated session to a safe next path', async () => {
  vi.stubGlobal('fetch', vi.fn().mockResolvedValue(jsonResponse(200, { providers: ['google'] })))
  renderLogin('/login?next=%2Fcollection', { authed: true })
  expect(await screen.findByLabelText('location')).toHaveTextContent('/collection')
})

it('sends an authenticated session home when next is unsafe', async () => {
  vi.stubGlobal('fetch', vi.fn().mockResolvedValue(jsonResponse(200, { providers: ['google'] })))
  renderLogin(`/login?next=${encodeURIComponent('//evil.example')}`, { authed: true })
  const probe = await screen.findByLabelText('location')
  expect(probe).toHaveTextContent('/')
  expect(probe).not.toHaveTextContent('evil')
})

it('does not stash when next is absent', async () => {
  vi.stubGlobal('fetch', vi.fn().mockResolvedValue(jsonResponse(200, { providers: ['google'] })))
  renderLogin('/login')
  const link = await screen.findByRole('link', { name: 'Continue with Google' })
  await clickWithoutNavigating(link)
  expect(sessionStorage.getItem('vg_next')).toBeNull()
})
