import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { render, screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { MemoryRouter } from 'react-router'
import Login from './Login'

const jsonResponse = (status: number, body: unknown) =>
  new Response(JSON.stringify(body), {
    status,
    headers: { 'Content-Type': 'application/json' },
  })

function renderLogin(path = '/login') {
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } })
  return render(
    <QueryClientProvider client={qc}>
      <MemoryRouter initialEntries={[path]}>
        <Login />
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

it('renders the logo mark before the title, decorative only', () => {
  vi.stubGlobal('fetch', vi.fn().mockResolvedValue(
    jsonResponse(200, { providers: ['dev'] })))
  renderLogin()
  const heading = screen.getByRole('heading', { name: 'vg-collect' })
  const mark = heading.previousElementSibling
  expect(mark?.tagName.toLowerCase()).toBe('svg')
  // The h1 carries the accessible name; the mark must stay silent.
  expect(mark).toHaveAttribute('aria-hidden', 'true')
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

it('does not stash when next is absent', async () => {
  vi.stubGlobal('fetch', vi.fn().mockResolvedValue(jsonResponse(200, { providers: ['google'] })))
  renderLogin('/login')
  const link = await screen.findByRole('link', { name: 'Continue with Google' })
  await clickWithoutNavigating(link)
  expect(sessionStorage.getItem('vg_next')).toBeNull()
})
