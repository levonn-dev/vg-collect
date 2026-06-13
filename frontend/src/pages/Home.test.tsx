import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { render, screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { MemoryRouter, Route, Routes } from 'react-router'
import Home from './Home'

const jsonResponse = (status: number, body: unknown) =>
  new Response(JSON.stringify(body), {
    status,
    headers: { 'Content-Type': 'application/json' },
  })

function renderHome() {
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } })
  return render(
    <QueryClientProvider client={qc}>
      <MemoryRouter initialEntries={['/']}>
        <Routes>
          <Route path="/" element={<Home />} />
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

it('renders the signed-in profile', async () => {
  vi.stubGlobal('fetch', vi.fn().mockResolvedValue(jsonResponse(200, me)))
  renderHome()
  expect(await screen.findByText('alice')).toBeInTheDocument()
  expect(screen.getByText('alice@example.test')).toBeInTheDocument()
  expect(screen.getByText(/roles: user/)).toBeInTheDocument()
})

it('bounces to login on 401', async () => {
  vi.stubGlobal('fetch', vi.fn().mockResolvedValue(jsonResponse(401, {
    type: 'about:blank', title: 'Unauthorized', status: 401, code: 'unauthenticated',
  })))
  renderHome()
  expect(await screen.findByText('login-page')).toBeInTheDocument()
})

it('shows an error state on non-auth failures', async () => {
  vi.stubGlobal('fetch', vi.fn().mockResolvedValue(new Response('x', { status: 502 })))
  renderHome()
  expect(await screen.findByRole('alert')).toHaveTextContent(/went wrong/i)
})

it('logs out and navigates to login', async () => {
  const fetchMock = vi.fn()
    .mockResolvedValueOnce(jsonResponse(200, me))
    .mockResolvedValueOnce(new Response(null, { status: 204 }))
  vi.stubGlobal('fetch', fetchMock)
  renderHome()
  await userEvent.click(await screen.findByRole('button', { name: 'Log out' }))
  expect(await screen.findByText('login-page')).toBeInTheDocument()
  expect(fetchMock).toHaveBeenCalledWith('/api/auth/logout', { method: 'POST' })
})

it('renders a decorative avatar when present', async () => {
  vi.stubGlobal('fetch', vi.fn().mockResolvedValue(jsonResponse(200, {
    ...me, avatar_url: 'https://cdn.example/a.png',
  })))
  const { container } = renderHome()
  // alt="" makes the avatar decorative, so it has no img role and no
  // accessible name: it is reachable only through the DOM, not by role.
  await screen.findByText('alice')
  const img = container.querySelector('img')
  expect(img).toHaveAttribute('src', 'https://cdn.example/a.png')
  expect(img).toHaveAttribute('alt', '') // decorative: the name is adjacent
})
