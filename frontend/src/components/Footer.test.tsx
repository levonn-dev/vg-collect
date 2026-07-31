import { render, screen, within } from '@testing-library/react'
import { MemoryRouter } from 'react-router'
import Footer from './Footer'

function renderFooter(showHelp = false) {
  return render(
    <MemoryRouter>
      <Footer showHelp={showHelp} />
    </MemoryRouter>,
  )
}

afterEach(() => {
  vi.unstubAllEnvs()
})

it('renders page links and the source link', () => {
  renderFooter()
  const footer = screen.getByRole('contentinfo', { name: 'Site footer' })
  expect(within(footer).getByRole('link', { name: 'About' })).toHaveAttribute('href', '/about')
  expect(within(footer).getByRole('link', { name: 'Terms' })).toHaveAttribute('href', '/terms')
  expect(within(footer).getByRole('link', { name: 'Privacy' })).toHaveAttribute('href', '/privacy')
  expect(within(footer).getByRole('link', { name: 'Source' })).toHaveAttribute(
    'href',
    'https://github.com/levonn-dev/vgkeep',
  )
  expect(within(footer).queryByRole('link', { name: 'Help' })).toBeNull()
})

it('shows the Help link only when asked', () => {
  renderFooter(true)
  expect(screen.getByRole('link', { name: 'Help' })).toHaveAttribute('href', '/help')
})

it('renders one credit line per active data source', () => {
  vi.stubEnv('VITE_SITE_DATA_SOURCES', 'igdb,frankfurter')
  renderFooter()
  expect(screen.getByText(/Game data provided by/)).toBeInTheDocument()
  expect(screen.getByRole('link', { name: 'IGDB' })).toHaveAttribute('href', 'https://www.igdb.com')
  expect(screen.getByText(/Exchange rates provided by/)).toBeInTheDocument()
  expect(screen.queryByText(/Price data/)).toBeNull()
})

it('renders no credits block when no source is active', () => {
  renderFooter()
  expect(screen.queryByText(/provided by/)).toBeNull()
})

it('omits the operator line until an operator is set', () => {
  renderFooter()
  expect(screen.queryByText(/is run by/)).toBeNull()
})

it('renders the operator line with a mailto contact', () => {
  vi.stubEnv('VITE_SITE_OPERATOR', 'Sam')
  vi.stubEnv('VITE_SITE_CONTACT', 'sam@example.test')
  renderFooter()
  expect(screen.getByText(/vgkeep is run by Sam/)).toBeInTheDocument()
  expect(screen.getByRole('link', { name: 'sam@example.test' })).toHaveAttribute(
    'href',
    'mailto:sam@example.test',
  )
})

it('renders the operator line without a contact link when only the operator is set', () => {
  vi.stubEnv('VITE_SITE_OPERATOR', 'Sam')
  renderFooter()
  expect(screen.getByText(/vgkeep is run by Sam/)).toBeInTheDocument()
  expect(document.querySelector('a[href^="mailto:"]')).toBeNull()
})
