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
