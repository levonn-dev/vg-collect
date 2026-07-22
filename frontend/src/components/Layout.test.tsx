import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { fireEvent, render, screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { MemoryRouter, Route, Routes } from 'react-router'
import { fxRatesFixture, jsonResponse } from '../test/fixtures'
import Layout from './Layout'

function renderLayout() {
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } })
  return render(
    <QueryClientProvider client={qc}>
      <MemoryRouter initialEntries={['/']}>
        <Routes>
          <Route element={<Layout />}>
            <Route path="/" element={<div>page-content</div>} />
          </Route>
          <Route path="/login" element={<div>login-page</div>} />
        </Routes>
      </MemoryRouter>
    </QueryClientProvider>,
  )
}

afterEach(() => vi.unstubAllGlobals())

const me = {
  id: 'u1', email: 'alice@example.test', display_name: 'alice', roles: ['user'],
}

it('renders the chrome and the routed page for a signed-in user', async () => {
  vi.stubGlobal('fetch', vi.fn().mockResolvedValue(jsonResponse(200, me)))
  renderLayout()
  expect(await screen.findByText('page-content')).toBeInTheDocument()
  expect(screen.getByText('alice')).toBeInTheDocument()
  expect(screen.getByRole('navigation', { name: 'Primary' })).toBeInTheDocument()
  expect(screen.getByRole('link', { name: 'Collection' })).toBeInTheDocument()
})

it('renders the logo mark before the title, decorative only', async () => {
  vi.stubGlobal('fetch', vi.fn().mockResolvedValue(jsonResponse(200, me)))
  renderLayout()
  await screen.findByText('page-content')
  const heading = screen.getByRole('heading', { name: 'vg-collect' })
  const mark = heading.previousElementSibling
  expect(mark?.tagName.toLowerCase()).toBe('svg')
  // The h1 carries the accessible name; the mark must stay silent.
  expect(mark).toHaveAttribute('aria-hidden', 'true')
})

it('links the identity block to the account page', async () => {
  vi.stubGlobal('fetch', vi.fn().mockResolvedValue(jsonResponse(200, me)))
  renderLayout()
  await screen.findByText('page-content')
  expect(screen.getByRole('link', { name: 'Account' })).toHaveAttribute('href', '/account')
})

it('renders the avatar without a referrer and falls back to an initial on load failure', async () => {
  vi.stubGlobal('fetch', vi.fn().mockResolvedValue(jsonResponse(200, {
    ...me, avatar_url: 'https://lh3.example/avatar=s96-c',
  })))
  renderLayout()
  await screen.findByText('page-content')
  const img = document.querySelector('img')
  expect(img).toHaveAttribute('src', 'https://lh3.example/avatar=s96-c')
  expect(img).toHaveAttribute('referrerpolicy', 'no-referrer')
  // Avatar hosts flake; a failed load must degrade to the initial
  // instead of a stuck blank image.
  fireEvent.error(img!)
  expect(document.querySelector('img')).toBeNull()
  expect(screen.getByText('a', { selector: 'span' })).toBeInTheDocument()
})

it('shows the initial when the profile has no avatar', async () => {
  vi.stubGlobal('fetch', vi.fn().mockResolvedValue(jsonResponse(200, me)))
  renderLayout()
  await screen.findByText('page-content')
  expect(document.querySelector('img')).toBeNull()
  expect(screen.getByText('a', { selector: 'span' })).toBeInTheDocument()
})

it('bounces to login on 401', async () => {
  vi.stubGlobal('fetch', vi.fn().mockResolvedValue(jsonResponse(401, {
    type: 'about:blank', title: 'Unauthorized', status: 401, code: 'unauthenticated',
  })))
  renderLayout()
  expect(await screen.findByText('login-page')).toBeInTheDocument()
})

it('shows an error state on non-auth failures', async () => {
  vi.stubGlobal('fetch', vi.fn().mockResolvedValue(new Response('x', { status: 502 })))
  renderLayout()
  expect(await screen.findByRole('alert')).toHaveTextContent(/went wrong/i)
})

it('logs out and navigates to login', async () => {
  // Routed by path rather than call order: CurrencySelect (mounted in
  // the header once /api/me resolves) fetches /api/fx, and, because
  // this client's default staleTime is 0, its own /api/me observer
  // triggers a background refetch too - so /api/me must tolerate
  // being called more than once.
  const fetchMock = vi.fn((path: string) => {
    if (path === '/api/fx') return Promise.resolve(jsonResponse(200, fxRatesFixture()))
    if (path === '/api/auth/logout') return Promise.resolve(new Response(null, { status: 204 }))
    return Promise.resolve(jsonResponse(200, me))
  })
  vi.stubGlobal('fetch', fetchMock)
  renderLayout()
  await userEvent.click(await screen.findByRole('button', { name: 'Log out' }))
  expect(await screen.findByText('login-page')).toBeInTheDocument()
  expect(fetchMock).toHaveBeenCalledWith('/api/auth/logout', { method: 'POST' })
})

it('shows the Admin nav link only for the admin role', async () => {
  vi.stubGlobal('fetch', vi.fn().mockResolvedValue(jsonResponse(200, { ...me, roles: ['user', 'admin'] })))
  renderLayout()
  await screen.findByText('page-content')
  expect(screen.getByRole('link', { name: 'Admin' })).toHaveAttribute('href', '/admin')
})

it('hides the Admin nav link without the admin role', async () => {
  vi.stubGlobal('fetch', vi.fn().mockResolvedValue(jsonResponse(200, me)))
  renderLayout()
  await screen.findByText('page-content')
  expect(screen.queryByRole('link', { name: 'Admin' })).not.toBeInTheDocument()
})
