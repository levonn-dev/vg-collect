import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { fireEvent, screen, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { MemoryRouter, Route, Routes, useLocation } from 'react-router'
import Home from '../pages/Home'
import { fxRatesFixture, jsonResponse, meFixture, problemResponse, requestPath } from '../test/fixtures'
import { renderWithI18n } from '../test/i18n'
import Layout from './Layout'

// Renders the landed query string so a test can assert the exact ?next= value.
function LoginProbe() {
  const location = useLocation()
  return <div>login-page{location.search}</div>
}

function renderLayout(path = '/') {
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } })
  return renderWithI18n(
    <QueryClientProvider client={qc}>
      <MemoryRouter initialEntries={[path]}>
        <Routes>
          <Route element={<Layout />}>
            <Route path="/" element={<div>page-content</div>} />
            <Route path="/entries/:id" element={<div>entry-detail-page</div>} />
          </Route>
          <Route path="/login" element={<LoginProbe />} />
        </Routes>
      </MemoryRouter>
    </QueryClientProvider>,
  )
}

afterEach(() => {
  vi.unstubAllGlobals()
  sessionStorage.clear()
})

const me = {
  id: 'u1', email: 'alice@example.test', handle: 'alice', roles: ['user'],
}

it('renders the chrome and the routed page for a signed-in user', async () => {
  vi.stubGlobal('fetch', vi.fn().mockResolvedValue(jsonResponse(200, me)))
  renderLayout()
  expect(await screen.findByText('page-content')).toBeInTheDocument()
  expect(screen.getByText('@alice')).toBeInTheDocument()
  expect(screen.getByRole('navigation', { name: 'Primary' })).toBeInTheDocument()
  expect(screen.getByRole('link', { name: 'Collection' })).toBeInTheDocument()
})

it('tabs to a skip link before anything else, targeting #main-content', async () => {
  // Fresh Response per call: staleTime 0 triggers a background /api/me
  // refetch, and a Response body can only be read once.
  vi.stubGlobal('fetch', vi.fn((path: unknown) => {
    if (requestPath(path) === '/api/fx') return Promise.resolve(jsonResponse(200, fxRatesFixture()))
    return Promise.resolve(jsonResponse(200, me))
  }))
  renderLayout()
  await screen.findByText('page-content')
  await userEvent.tab()
  const skipLink = screen.getByRole('link', { name: 'Skip to content' })
  expect(skipLink).toHaveAttribute('href', '#main-content')
  expect(document.activeElement).toBe(skipLink)
})

it('renders the Explore nav link after Add', async () => {
  vi.stubGlobal('fetch', vi.fn().mockResolvedValue(jsonResponse(200, me)))
  renderLayout()
  await screen.findByText('page-content')
  expect(screen.getByRole('link', { name: 'Explore' })).toHaveAttribute('href', '/explore')
})

it('renders the Feed nav link after Explore', async () => {
  vi.stubGlobal('fetch', vi.fn().mockResolvedValue(jsonResponse(200, me)))
  renderLayout()
  await screen.findByText('page-content')
  expect(screen.getByRole('link', { name: 'Feed' })).toHaveAttribute('href', '/feed')
})

it('renders the logo mark before the title, decorative only', async () => {
  vi.stubGlobal('fetch', vi.fn().mockResolvedValue(jsonResponse(200, me)))
  renderLayout()
  await screen.findByText('page-content')
  const heading = screen.getByRole('heading', { name: 'vgkeep' })
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
  // Avatar hosts flake; a failed load must degrade to the initial, not a blank.
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
  vi.stubGlobal('fetch', vi.fn().mockResolvedValue(problemResponse(401, 'unauthenticated')))
  renderLayout()
  expect(await screen.findByText('login-page')).toBeInTheDocument()
})

it('carries the attempted deep path as ?next= on the login redirect', async () => {
  vi.stubGlobal('fetch', vi.fn().mockResolvedValue(problemResponse(401, 'unauthenticated')))
  renderLayout('/entries/abc')
  expect(await screen.findByText(`login-page?next=${encodeURIComponent('/entries/abc')}`))
    .toBeInTheDocument()
})

it('consumes a stashed next path once the profile resolves', async () => {
  // Fresh Response per path: CurrencySelect also fetches /api/fx, and a
  // shared Response can't have its body read twice.
  sessionStorage.setItem('vg_next', '/entries/abc')
  vi.stubGlobal('fetch', vi.fn((path: unknown) => {
    if (requestPath(path) === '/api/fx') return Promise.resolve(jsonResponse(200, fxRatesFixture()))
    return Promise.resolve(jsonResponse(200, me))
  }))
  renderLayout()
  expect(await screen.findByText('entry-detail-page')).toBeInTheDocument()
  expect(sessionStorage.getItem('vg_next')).toBeNull()
})

// Home redirects on mount to the landing_page pref; a stashed next path must
// still win. That holds only because Home's <Navigate> effect (a Layout
// descendant via Outlet) fires before Layout's stash effect, so mount both.
function renderLayoutWithHome() {
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false, staleTime: Infinity } } })
  qc.setQueryData(['me'], meFixture({ landing_page: 'collection' }))
  return renderWithI18n(
    <QueryClientProvider client={qc}>
      <MemoryRouter initialEntries={['/']}>
        <Routes>
          <Route element={<Layout />}>
            <Route path="/" element={<Home />} />
            <Route path="/collection" element={<div>collection-page</div>} />
            <Route path="/account" element={<div>account-page</div>} />
          </Route>
        </Routes>
      </MemoryRouter>
    </QueryClientProvider>,
  )
}

it('lets a stashed next path win over the Home landing redirect', async () => {
  sessionStorage.setItem('vg_next', '/account')
  vi.stubGlobal('fetch', vi.fn((path: unknown) => {
    if (requestPath(path) === '/api/fx') return Promise.resolve(jsonResponse(200, fxRatesFixture()))
    return Promise.resolve(jsonResponse(200, meFixture({ landing_page: 'collection' })))
  }))
  renderLayoutWithHome()
  expect(await screen.findByText('account-page')).toBeInTheDocument()
  expect(sessionStorage.getItem('vg_next')).toBeNull()
})

it('falls through to the Home landing redirect with no stash present', async () => {
  vi.stubGlobal('fetch', vi.fn((path: unknown) => {
    if (requestPath(path) === '/api/fx') return Promise.resolve(jsonResponse(200, fxRatesFixture()))
    return Promise.resolve(jsonResponse(200, meFixture({ landing_page: 'collection' })))
  }))
  renderLayoutWithHome()
  expect(await screen.findByText('collection-page')).toBeInTheDocument()
})

it('shows an error state on non-auth failures', async () => {
  vi.stubGlobal('fetch', vi.fn().mockResolvedValue(new Response('x', { status: 502 })))
  renderLayout()
  expect(await screen.findByRole('alert')).toHaveTextContent(/went wrong/i)
})

it('logs out and navigates to login', async () => {
  // Routed by path, not call order: CurrencySelect also fetches /api/fx, and
  // staleTime 0 triggers a background /api/me refetch too.
  const fetchMock = vi.fn((path: unknown) => {
    if (requestPath(path) === '/api/fx') return Promise.resolve(jsonResponse(200, fxRatesFixture()))
    if (requestPath(path) === '/api/auth/logout') return Promise.resolve(new Response(null, { status: 204 }))
    return Promise.resolve(jsonResponse(200, me))
  })
  vi.stubGlobal('fetch', fetchMock)
  renderLayout()
  await userEvent.click(await screen.findByRole('button', { name: 'Log out' }))
  expect(await screen.findByText('login-page')).toBeInTheDocument()
  expect(fetchMock.mock.calls.some(
    (c) => requestPath(c[0]) === '/api/auth/logout' && (c[0] as Request).method === 'POST',
  )).toBe(true)
})

it('clears the cache and navigates to login even when the logout request fails', async () => {
  // onSettled fires on success and failure alike; a failed logout still
  // leaves the session gone or unreachable.
  const fetchMock = vi.fn((path: unknown) => {
    if (requestPath(path) === '/api/fx') return Promise.resolve(jsonResponse(200, fxRatesFixture()))
    if (requestPath(path) === '/api/auth/logout') return Promise.resolve(new Response('x', { status: 500 }))
    return Promise.resolve(jsonResponse(200, me))
  })
  vi.stubGlobal('fetch', fetchMock)
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } })
  // clear() on the still-mounted me observer triggers a refetch that races
  // navigate's unmount, so assert the clear ran, not the post-clear cache.
  const clearSpy = vi.spyOn(qc, 'clear')
  renderWithI18n(
    <QueryClientProvider client={qc}>
      <MemoryRouter initialEntries={['/']}>
        <Routes>
          <Route element={<Layout />}>
            <Route path="/" element={<div>page-content</div>} />
          </Route>
          <Route path="/login" element={<LoginProbe />} />
        </Routes>
      </MemoryRouter>
    </QueryClientProvider>,
  )
  await screen.findByText('page-content')
  expect(qc.getQueryData(['me'])).toBeDefined()
  await userEvent.click(screen.getByRole('button', { name: 'Log out' }))
  expect(await screen.findByText('login-page')).toBeInTheDocument()
  expect(clearSpy).toHaveBeenCalled()
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

it('renders the site footer with the Help link', async () => {
  vi.stubGlobal('fetch', vi.fn().mockResolvedValue(jsonResponse(200, me)))
  renderLayout()
  await screen.findByText('page-content')
  const footer = screen.getByRole('contentinfo', { name: 'Site footer' })
  expect(within(footer).getByRole('link', { name: 'Help' })).toHaveAttribute('href', '/help')
})

it('mounts the language switcher once for a signed-in user, in the footer', async () => {
  vi.stubGlobal('fetch', vi.fn().mockResolvedValue(jsonResponse(200, me)))
  renderLayout()
  await screen.findByText('page-content')
  // One mount point for the whole app: the app bar carries no copy of it.
  expect(screen.getAllByRole('combobox', { name: 'Language' })).toHaveLength(1)
  const footer = screen.getByRole('contentinfo', { name: 'Site footer' })
  expect(within(footer).getByRole('combobox', { name: 'Language' })).toBeInTheDocument()
})

it('links the brand back to the start page', async () => {
  vi.stubGlobal('fetch', vi.fn().mockResolvedValue(jsonResponse(200, me)))
  renderLayout()
  await screen.findByText('page-content')
  expect(screen.getByRole('link', { name: 'vgkeep' })).toHaveAttribute('href', '/')
})
