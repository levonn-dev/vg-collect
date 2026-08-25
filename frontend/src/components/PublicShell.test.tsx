import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { screen, waitFor, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { MemoryRouter, Route, Routes } from 'react-router'
import { jsonResponse } from '../test/fixtures'
import { renderWithI18n } from '../test/i18n'
import PublicShell from './PublicShell'

function renderShell(path = '/about') {
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } })
  return renderWithI18n(
    <QueryClientProvider client={qc}>
      <MemoryRouter initialEntries={[path]}>
        <Routes>
          <Route element={<PublicShell />}>
            <Route path="/about" element={<div>public-page</div>} />
          </Route>
        </Routes>
      </MemoryRouter>
    </QueryClientProvider>,
  )
}

// A fetch that never settles: the shell must not wait on the session
// probe to paint.
function stubPendingFetch() {
  vi.stubGlobal('fetch', vi.fn().mockReturnValue(new Promise(() => {})))
}

afterEach(() => {
  vi.unstubAllGlobals()
  vi.unstubAllEnvs()
})

const me = {
  id: 'u1', email: 'alice@example.test', handle: 'alice', roles: ['user'],
}

it('renders page, brand header, and footer before the session probe settles', () => {
  stubPendingFetch()
  renderShell()
  expect(screen.getByText('public-page')).toBeInTheDocument()
  expect(screen.getByRole('heading', { name: 'vgkeep' })).toBeInTheDocument()
  expect(screen.getByRole('contentinfo', { name: 'Site footer' })).toBeInTheDocument()
  expect(screen.queryByRole('navigation', { name: 'Primary' })).toBeNull()
})

it('tabs to a skip link before anything else, targeting #main-content', async () => {
  stubPendingFetch()
  renderShell()
  await userEvent.tab()
  const skipLink = screen.getByRole('link', { name: 'Skip to content' })
  expect(skipLink).toHaveAttribute('href', '#main-content')
  expect(document.activeElement).toBe(skipLink)
})

it('keeps the brand-only header and hides Help when the probe answers 401', async () => {
  const fetchSpy = vi.fn().mockResolvedValue(jsonResponse(401, { title: 'unauthorized' }))
  vi.stubGlobal('fetch', fetchSpy)
  renderShell()
  await waitFor(() => expect(fetchSpy).toHaveBeenCalled())
  expect(screen.getByRole('heading', { name: 'vgkeep' })).toBeInTheDocument()
  expect(screen.queryByRole('navigation', { name: 'Primary' })).toBeNull()
  expect(screen.queryByRole('link', { name: 'Help' })).toBeNull()
})

it('swaps in the signed-in app bar and Help link when a session exists', async () => {
  vi.stubGlobal('fetch', vi.fn().mockResolvedValue(jsonResponse(200, me)))
  renderShell()
  expect(await screen.findByRole('navigation', { name: 'Primary' })).toBeInTheDocument()
  expect(screen.getByText('@alice')).toBeInTheDocument()
  expect(screen.getByRole('link', { name: 'Help' })).toHaveAttribute('href', '/help')
  expect(screen.getByText('public-page')).toBeInTheDocument()
})

it('offers the language switcher to a visitor with no session', async () => {
  const fetchSpy = vi.fn().mockResolvedValue(jsonResponse(401, { title: 'unauthorized' }))
  vi.stubGlobal('fetch', fetchSpy)
  renderShell()
  await waitFor(() => expect(fetchSpy).toHaveBeenCalled())
  expect(screen.queryByRole('navigation', { name: 'Primary' })).toBeNull()
  const select = screen.getByRole('combobox', { name: 'Language' })
  // Each option carries the language its endonym is written in, so a
  // screen reader pronounces it that way for a signed-out visitor too.
  expect(within(select).getByRole('option', { name: 'English' })).toHaveAttribute('lang', 'en')
  expect(within(select).getByRole('option', { name: '日本語' })).toHaveAttribute('lang', 'ja')
})

it('links the brand to the start page', () => {
  stubPendingFetch()
  renderShell()
  expect(screen.getByRole('link', { name: 'vgkeep' })).toHaveAttribute('href', '/')
})

it('renders the configured site name', () => {
  stubPendingFetch()
  vi.stubEnv('VITE_SITE_NAME', 'MyShelf')
  renderShell()
  expect(screen.getByRole('heading', { name: 'MyShelf' })).toBeInTheDocument()
})

it('keeps the logo mark decorative', () => {
  stubPendingFetch()
  renderShell()
  const heading = screen.getByRole('heading', { name: 'vgkeep' })
  const mark = heading.parentElement?.querySelector('svg')
  expect(mark).toHaveAttribute('aria-hidden', 'true')
})
