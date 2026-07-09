import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { render, screen } from '@testing-library/react'
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

afterEach(() => vi.unstubAllGlobals())

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
