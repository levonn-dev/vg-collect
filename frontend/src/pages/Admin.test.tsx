import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { MemoryRouter, Route, Routes } from 'react-router'
import { jsonResponse, requestPath } from '../test/fixtures'
import { renderWithI18n } from '../test/i18n'
import Admin from './Admin'

function renderAdmin() {
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } })
  return renderWithI18n(
    <QueryClientProvider client={qc}>
      <MemoryRouter initialEntries={['/admin']}>
        <Routes>
          <Route path="/admin" element={<Admin />} />
          <Route path="/" element={<div>home-page</div>} />
        </Routes>
      </MemoryRouter>
    </QueryClientProvider>,
  )
}

// Fetch is routed per endpoint (first matching prefix wins) so each
// test declares exactly the calls it expects; a URL nothing stubbed
// is recorded and fails the test in afterEach.
let unstubbed: string[] = []
function stubFetch(routes: Record<string, unknown>) {
  const impl = vi.fn().mockImplementation((url: unknown) => {
    const hit = Object.entries(routes).find(([prefix]) => requestPath(url).startsWith(prefix))
    if (!hit) {
      unstubbed.push(requestPath(url))
      return Promise.reject(new Error(`unstubbed fetch: ${requestPath(url)}`))
    }
    return Promise.resolve(jsonResponse(200, hit[1]))
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

const adminMe = {
  id: 'u1', email: 'admin@example.test', handle: 'admin', roles: ['user', 'admin'],
}
const emptyProducts = { products: [], total_count: 0 }

it('renders the admin console for the admin role', async () => {
  stubFetch({
    '/api/me': adminMe,
    '/api/admin/products/unmatched': emptyProducts,
    '/api/admin/products/promote-candidates': emptyProducts,
    '/api/admin/normalize-platforms': { scanned: 0, normalized: 0, skipped: 0 },
    '/api/admin/normalize-regions': { scanned: 0, normalized: 0, skipped: 0 },
    '/api/admin/normalize-community-regions': { scanned: 0, normalized: 0, skipped: 0 },
  })
  renderAdmin()
  expect(await screen.findByRole('heading', { name: 'Admin' })).toBeInTheDocument()
  expect(screen.getByRole('heading', { name: 'Maintenance' })).toBeInTheDocument()
  expect(screen.getByRole('button', { name: 'Trigger catalog refresh' })).toBeInTheDocument()
  expect(screen.getByRole('button', { name: 'Trigger entry rematch' })).toBeInTheDocument()
  expect(screen.getByRole('button', { name: 'Run entry resnapshot' })).toBeInTheDocument()
  expect(screen.getByRole('button', { name: 'Run platform normalization' })).toBeInTheDocument()
  expect(screen.getByRole('button', { name: 'Run region normalization' })).toBeInTheDocument()
  expect(screen.getByRole('button', { name: 'Run community region normalization' })).toBeInTheDocument()
})

it('redirects non-admins home without fetching admin data', async () => {
  stubFetch({ '/api/me': { ...adminMe, roles: ['user'] } })
  renderAdmin()
  expect(await screen.findByText('home-page')).toBeInTheDocument()
  expect(screen.queryByRole('heading', { name: 'Admin' })).not.toBeInTheDocument()
})

it('renders two tabs and switches to Submissions', async () => {
  stubFetch({
    '/api/me': adminMe,
    '/api/admin/products/unmatched': emptyProducts,
    '/api/admin/products/promote-candidates': emptyProducts,
    '/api/admin/products/community': emptyProducts,
    '/api/admin/submissions': { submissions: [], total_count: 0 },
  })
  renderAdmin()
  await userEvent.click(await screen.findByRole('tab', { name: 'Submissions' }))
  expect(await screen.findByText('0 pending submissions')).toBeInTheDocument()
  expect(screen.queryByRole('region', { name: 'Unmatched products' })).not.toBeInTheDocument()
})
