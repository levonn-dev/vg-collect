import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { render, screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { MemoryRouter, Route, Routes } from 'react-router'
import { jsonResponse } from '../test/fixtures'
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
  const fetchMock = vi.fn()
    .mockResolvedValueOnce(jsonResponse(200, me))
    .mockResolvedValueOnce(new Response(null, { status: 204 }))
  vi.stubGlobal('fetch', fetchMock)
  renderLayout()
  await userEvent.click(await screen.findByRole('button', { name: 'Log out' }))
  expect(await screen.findByText('login-page')).toBeInTheDocument()
  expect(fetchMock).toHaveBeenCalledWith('/api/auth/logout', { method: 'POST' })
})
