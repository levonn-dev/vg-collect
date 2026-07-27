import { render, screen } from '@testing-library/react'
import App from './App'

const jsonResponse = (status: number, body: unknown) =>
  new Response(JSON.stringify(body), {
    status,
    headers: { 'Content-Type': 'application/json' },
  })

afterEach(() => {
  vi.unstubAllGlobals()
  window.history.pushState({}, '', '/')
})

it('boots into the app shell', async () => {
  window.history.pushState({}, '', '/login')
  vi.stubGlobal('fetch', vi.fn().mockResolvedValue(jsonResponse(200, { providers: [] })))
  render(<App />)
  expect(await screen.findByText('vg-collect')).toBeInTheDocument()
})

it('does not retry a 401 and routes to login', async () => {
  window.history.pushState({}, '', '/')
  const fetchMock = vi.fn().mockResolvedValue(jsonResponse(401, {
    type: 'about:blank', title: 'Unauthorized', status: 401, code: 'unauthenticated',
  }))
  vi.stubGlobal('fetch', fetchMock)
  render(<App />)
  // The login route renders its own provider buttons region.
  expect(await screen.findByText('Track your game collection.')).toBeInTheDocument()
  // 401 must not be retried: /api/me is hit exactly once.
  const meCalls = fetchMock.mock.calls.filter((c) => c[0] === '/api/me')
  expect(meCalls).toHaveLength(1)
})

// The header's CurrencySelect fetches /api/fx on every authenticated
// page; without a rates-shaped stub it renders past an empty body and
// crashes reading .rates off it, taking the whole shell down. App's
// QueryClient is a module singleton shared by every test in this
// file (no per-test reset), and fx's staleTime is an hour, so a bad
// body cached by one test would poison every later one too - every
// authenticated-page test here must stub /api/fx explicitly.
const fxRates = { base: 'USD', date: '2026-01-01', rates: { EUR: 0.9 } }

it('renders the account page inside the shell', async () => {
  window.history.pushState({}, '', '/account')
  vi.stubGlobal('fetch', vi.fn((input: string) => {
    const url = String(input)
    if (url === '/api/me/identities') return Promise.resolve(jsonResponse(200, { identities: [] }))
    if (url === '/api/auth/providers') return Promise.resolve(jsonResponse(200, { providers: [] }))
    if (url === '/api/fx') return Promise.resolve(jsonResponse(200, fxRates))
    return Promise.resolve(jsonResponse(200, {
      id: 'u1', email: 'alice@example.test', handle: 'alice', roles: ['user'],
    }))
  }))
  render(<App />)
  expect(await screen.findByRole('heading', { name: 'Account' })).toBeInTheDocument()
})

it('renders the explore page inside the shell', async () => {
  window.history.pushState({}, '', '/explore')
  vi.stubGlobal('fetch', vi.fn((input: string) => {
    const url = String(input)
    if (url.startsWith('/api/explore')) return Promise.resolve(jsonResponse(200, { shelves: [], total_count: 0 }))
    if (url === '/api/fx') return Promise.resolve(jsonResponse(200, fxRates))
    return Promise.resolve(jsonResponse(200, {
      id: 'u1', email: 'alice@example.test', handle: 'alice', roles: ['user'],
    }))
  }))
  render(<App />)
  expect(await screen.findByRole('heading', { name: 'Explore' })).toBeInTheDocument()
})

it('renders the profile page inside the shell', async () => {
  window.history.pushState({}, '', '/u/Alice_Prime')
  vi.stubGlobal('fetch', vi.fn((input: string) => {
    const url = String(input)
    if (url.startsWith('/api/profiles/')) {
      return Promise.resolve(jsonResponse(200, {
        profile: { user_id: 'u2', handle: 'Alice_Prime', profile_visibility: 'listed' },
        social_available: true,
        social: { follower_count: 0, following_count: 0, viewer_follows: false },
        shelves: [],
        total_count: 0,
      }))
    }
    if (url === '/api/fx') return Promise.resolve(jsonResponse(200, fxRates))
    return Promise.resolve(jsonResponse(200, {
      id: 'u1', email: 'alice@example.test', handle: 'alice', roles: ['user'],
    }))
  }))
  render(<App />)
  expect(await screen.findByRole('heading', { name: '@Alice_Prime' })).toBeInTheDocument()
})

it('renders the shared shelf page inside the shell', async () => {
  window.history.pushState({}, '', '/u/Alice_Prime/shelves/backlog-wall')
  vi.stubGlobal('fetch', vi.fn((input: string) => {
    const url = String(input)
    if (url.startsWith('/api/profiles/Alice_Prime/shelves/backlog-wall')) {
      return Promise.resolve(jsonResponse(200, {
        shelf: { id: 'shelf1', name: 'Backlog Wall', slug: 'backlog-wall', params: { mode: 'table' } },
        owner: { user_id: 'u2', handle: 'Alice_Prime', profile_visibility: 'listed' },
        social_available: true,
        social: { shelf_id: 'shelf1', like_count: 0, comment_count: 0, viewer_likes: false },
      }))
    }
    if (url.startsWith('/api/shelves/shelf1/entries')) {
      return Promise.resolve(jsonResponse(200, { total_count: 0, entries: [] }))
    }
    if (url.startsWith('/api/shelves/shelf1/comments')) {
      return Promise.resolve(jsonResponse(200, { comments: [] }))
    }
    if (url === '/api/fx') return Promise.resolve(jsonResponse(200, fxRates))
    return Promise.resolve(jsonResponse(200, {
      id: 'u1', email: 'alice@example.test', handle: 'alice', roles: ['user'],
    }))
  }))
  render(<App />)
  expect(await screen.findByRole('heading', { name: 'Backlog Wall' })).toBeInTheDocument()
})

it('renders the feed page inside the shell', async () => {
  window.history.pushState({}, '', '/feed')
  vi.stubGlobal('fetch', vi.fn((input: string) => {
    const url = String(input)
    if (url.startsWith('/api/feed')) return Promise.resolve(jsonResponse(200, { items: [] }))
    if (url === '/api/fx') return Promise.resolve(jsonResponse(200, fxRates))
    return Promise.resolve(jsonResponse(200, {
      id: 'u1', email: 'alice@example.test', handle: 'alice', roles: ['user'],
    }))
  }))
  render(<App />)
  expect(await screen.findByRole('heading', { name: 'Feed' })).toBeInTheDocument()
})
