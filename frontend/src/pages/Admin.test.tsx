import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { render, screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { MemoryRouter, Route, Routes } from 'react-router'
import { jsonResponse } from '../test/fixtures'
import Admin from './Admin'

function renderAdmin() {
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } })
  return render(
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

afterEach(() => vi.unstubAllGlobals())

const adminMe = {
  id: 'u1', email: 'admin@example.test', display_name: 'admin', roles: ['user', 'admin'],
}

it('renders the admin console for the admin role', async () => {
  vi.stubGlobal('fetch', vi.fn().mockResolvedValue(jsonResponse(200, adminMe)))
  renderAdmin()
  expect(await screen.findByRole('heading', { name: 'Admin' })).toBeInTheDocument()
})

it('redirects non-admins home without flashing admin UI', async () => {
  vi.stubGlobal('fetch', vi.fn().mockResolvedValue(jsonResponse(200, { ...adminMe, roles: ['user'] })))
  renderAdmin()
  expect(await screen.findByText('home-page')).toBeInTheDocument()
  expect(screen.queryByRole('heading', { name: 'Admin' })).not.toBeInTheDocument()
})

it('renders two tabs and switches to Submissions', async () => {
  vi.stubGlobal('fetch', vi.fn().mockImplementation((url: string) => {
    if (url === '/api/me') return Promise.resolve(jsonResponse(200, adminMe))
    if (url.startsWith('/api/admin/submissions')) return Promise.resolve(jsonResponse(200, { submissions: [], total_count: 0 }))
    return Promise.resolve(jsonResponse(200, { products: [], total_count: 0 }))
  }))
  renderAdmin()
  await userEvent.click(await screen.findByRole('tab', { name: 'Submissions' }))
  expect(await screen.findByText('0 pending submissions')).toBeInTheDocument()
  expect(screen.queryByRole('region', { name: 'Unmatched products' })).not.toBeInTheDocument()
})
