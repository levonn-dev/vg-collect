import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { render, screen } from '@testing-library/react'
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
