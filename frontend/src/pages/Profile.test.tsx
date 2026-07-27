import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { fireEvent, render, screen, within } from '@testing-library/react'
import { MemoryRouter, Route, Routes } from 'react-router'
import type { ProfilePage } from '../api/social'
import { jsonResponse } from '../test/fixtures'
import Profile from './Profile'

function renderProfile(handle = 'Alice_Prime') {
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } })
  return render(
    <QueryClientProvider client={qc}>
      <MemoryRouter initialEntries={[`/u/${handle}`]}>
        <Routes>
          <Route path="/u/:handle" element={<Profile />} />
        </Routes>
      </MemoryRouter>
    </QueryClientProvider>,
  )
}

// Same route-map idiom as Admin.test/Explore.test: fetch is dispatched
// by matching prefix, and any URL nothing stubbed fails the test in
// afterEach. A route's value may be a plain body (always 200) or a
// Response for an explicit status (the 404/502 tests).
let unstubbed: string[] = []
function stubFetch(routes: Record<string, unknown>) {
  const impl = vi.fn().mockImplementation((url: string) => {
    const hit = Object.entries(routes).find(([prefix]) => String(url).startsWith(prefix))
    if (!hit) {
      unstubbed.push(String(url))
      return Promise.reject(new Error(`unstubbed fetch: ${String(url)}`))
    }
    const value = hit[1]
    return Promise.resolve(value instanceof Response ? value : jsonResponse(200, value))
  })
  vi.stubGlobal('fetch', impl)
  return impl
}

afterEach(() => {
  vi.unstubAllGlobals()
  const missed = unstubbed
  unstubbed = []
  expect(missed).toEqual([])
})

const visitorMe = { id: 'u1', email: 'visitor@example.test', handle: 'visitor', roles: ['user'] }
const ownerMe = { ...visitorMe, id: 'u2', handle: 'alice' }

const shelf = {
  id: 's1', name: 'Backlog Wall', slug: 'backlog-wall',
  owner: { user_id: 'u2', handle: 'Alice_Prime', profile_visibility: 'listed' as const },
  entry_count: 4, cover_urls: [],
}

function profilePage(overrides: Partial<ProfilePage> = {}): ProfilePage {
  return {
    profile: { user_id: 'u2', handle: 'Alice_Prime', profile_visibility: 'listed' },
    social_available: true,
    social: { follower_count: 5, following_count: 3, viewer_follows: false },
    shelves: [shelf],
    total_count: 1,
    ...overrides,
  }
}

it('renders the owner card, follower counts, a follow button, and the shelf grid for a visitor', async () => {
  stubFetch({ '/api/me': visitorMe, '/api/profiles/Alice_Prime': profilePage() })
  renderProfile()
  expect(await screen.findByRole('heading', { name: '@Alice_Prime' })).toBeInTheDocument()
  expect(screen.getByText('5 followers - 3 following')).toBeInTheDocument()
  expect(screen.getByRole('button', { name: 'Follow' })).toBeInTheDocument()
  expect(screen.getByRole('link', { name: 'Backlog Wall' })).toBeInTheDocument()
})

it('singularizes a one-follower count and pluralizes a multi-shelf heading', async () => {
  const secondShelf = { ...shelf, id: 's2', name: 'Hall of Fame', slug: 'hall-of-fame' }
  stubFetch({
    '/api/me': visitorMe,
    '/api/profiles/Alice_Prime': profilePage({
      social: { follower_count: 1, following_count: 3, viewer_follows: false },
      shelves: [shelf, secondShelf],
      total_count: 2,
    }),
  })
  renderProfile()
  await screen.findByRole('heading', { name: '@Alice_Prime' })
  expect(screen.getByText('1 follower - 3 following')).toBeInTheDocument()
  expect(screen.getByText('2 shared shelves')).toBeInTheDocument()
})

it('hides the follow button while the viewer identity is still resolving, not just once known non-owner', async () => {
  // /api/me never resolves; /api/profiles/Alice_Prime does. isOwner
  // defaults to true (hidden) until me.data proves otherwise, so a
  // visitor's Follow button never flashes on before the owner check
  // catches up.
  vi.stubGlobal('fetch', vi.fn().mockImplementation((url: string) =>
    String(url).startsWith('/api/profiles/')
      ? Promise.resolve(jsonResponse(200, profilePage()))
      : new Promise(() => {}),
  ))
  renderProfile()
  await screen.findByRole('heading', { name: '@Alice_Prime' })
  expect(screen.queryByRole('button', { name: 'Follow' })).not.toBeInTheDocument()
})

it('renders the avatar image without a referrer, falling back to the initial on load failure', async () => {
  stubFetch({
    '/api/me': visitorMe,
    '/api/profiles/Alice_Prime': profilePage({
      profile: {
        user_id: 'u2', handle: 'Alice_Prime', profile_visibility: 'listed',
        avatar_url: 'https://img.example/a.png',
      },
    }),
  })
  renderProfile()
  const heading = await screen.findByRole('heading', { name: '@Alice_Prime' })
  // Scoped to the header: the shelf grid's own UserChip byline renders
  // the same fallback initial for the same handle, so an unscoped
  // query would match both once the header's avatar also falls back.
  const headerEl = heading.closest('header')!
  const header = within(headerEl)
  const img = headerEl.querySelector('img')!
  expect(img).toHaveAttribute('src', 'https://img.example/a.png')
  expect(img).toHaveAttribute('referrerpolicy', 'no-referrer')
  expect(img).toHaveAttribute('alt', '')
  fireEvent.error(img)
  expect(headerEl.querySelector('img')).toBeNull()
  expect(header.getByText('A', { selector: 'span[aria-hidden="true"]' })).toBeInTheDocument()
})

it('shows a loading state before the profile resolves', () => {
  vi.stubGlobal('fetch', vi.fn().mockImplementation(() => new Promise(() => {})))
  renderProfile()
  expect(screen.getByText(/loading profile/i)).toBeInTheDocument()
})

it('hides the follow button when the viewer is the profile owner', async () => {
  stubFetch({ '/api/me': ownerMe, '/api/profiles/Alice_Prime': profilePage() })
  renderProfile()
  await screen.findByRole('heading', { name: '@Alice_Prime' })
  expect(screen.queryByRole('button', { name: /Follow/ })).not.toBeInTheDocument()
})

it('hides counts and the follow button quietly when the social summary is unavailable', async () => {
  stubFetch({
    '/api/me': visitorMe,
    '/api/profiles/Alice_Prime': profilePage({ social_available: false, social: undefined }),
  })
  renderProfile()
  await screen.findByRole('heading', { name: '@Alice_Prime' })
  expect(screen.queryByText(/following/)).not.toBeInTheDocument()
  expect(screen.queryByRole('button', { name: /Follow/ })).not.toBeInTheDocument()
  expect(screen.queryByRole('alert')).not.toBeInTheDocument()
})

it('renders the empty-shelf state when the owner has published nothing', async () => {
  stubFetch({
    '/api/me': visitorMe,
    '/api/profiles/Alice_Prime': profilePage({ shelves: [], total_count: 0 }),
  })
  renderProfile()
  expect(await screen.findByText('No shared shelves yet.')).toBeInTheDocument()
})

it('shows a generic error state for a non-404 failure', async () => {
  stubFetch({ '/api/me': visitorMe, '/api/profiles/Alice_Prime': jsonResponse(502, {}) })
  renderProfile()
  expect(await screen.findByRole('alert')).toHaveTextContent(/cannot be loaded/i)
})

it('renders an identical not-found body for a private handle as for an unknown one - the DOM never leaks the distinction', async () => {
  stubFetch({
    '/api/me': visitorMe,
    '/api/profiles/Ghost': jsonResponse(404, {
      type: 'about:blank', title: 'Not Found', status: 404, code: 'profile_not_found',
    }),
  })
  const plain = renderProfile('Ghost')
  expect(await screen.findByRole('alert')).toHaveTextContent('Nothing here.')
  const plainHTML = plain.container.innerHTML
  plain.unmount()

  stubFetch({
    '/api/me': visitorMe,
    '/api/profiles/Hidden': jsonResponse(404, {
      type: 'about:blank', title: 'Not Found', status: 404, code: 'profile_not_found', detail: 'private',
    }),
  })
  const hidden = renderProfile('Hidden')
  await screen.findByRole('alert')
  // Same markup, not just the same text: a self-evident proof that
  // the private flavor renders no extra hint anywhere in the tree.
  expect(hidden.container.innerHTML).toBe(plainHTML)
})
