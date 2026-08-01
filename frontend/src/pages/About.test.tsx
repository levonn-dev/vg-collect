import { screen, within } from '@testing-library/react'
import { MemoryRouter } from 'react-router'
import { renderWithI18n } from '../test/i18n'
import About from './About'

function renderAbout() {
  return renderWithI18n(
    <MemoryRouter>
      <About />
    </MemoryRouter>,
  )
}

afterEach(() => {
  vi.unstubAllEnvs()
})

it('renders defaults when no slot is set', () => {
  renderAbout()
  expect(screen.getByRole('heading', { name: 'About vgkeep' })).toBeInTheDocument()
  expect(screen.getByText(/run by the operator of this instance/)).toBeInTheDocument()
  expect(
    screen.getByRole('link', { name: 'https://github.com/levonn-dev/vgkeep' }),
  ).toHaveAttribute('href', 'https://github.com/levonn-dev/vgkeep')
  expect(screen.queryByRole('region', { name: 'Data sources' })).toBeNull()
})

it('names the operator and links the contact when set', () => {
  vi.stubEnv('VITE_SITE_NAME', 'MyShelf')
  vi.stubEnv('VITE_SITE_OPERATOR', 'Sam')
  vi.stubEnv('VITE_SITE_CONTACT', 'sam@example.test')
  renderAbout()
  expect(screen.getByRole('heading', { name: 'About MyShelf' })).toBeInTheDocument()
  expect(screen.getByText(/run by Sam/)).toBeInTheDocument()
  expect(screen.getByRole('link', { name: 'sam@example.test' })).toHaveAttribute(
    'href',
    'mailto:sam@example.test',
  )
})

it('lists active data sources only', () => {
  vi.stubEnv('VITE_SITE_DATA_SOURCES', 'igdb')
  renderAbout()
  const region = screen.getByRole('region', { name: 'Data sources' })
  expect(within(region).getByRole('link', { name: 'IGDB' })).toBeInTheDocument()
  expect(within(region).queryByText(/PriceCharting/)).toBeNull()
})

it('omits the contact sentence when no contact is set', () => {
  renderAbout()
  expect(screen.queryByText(/Contact:/)).toBeNull()
  expect(document.querySelector('a[href^="mailto:"]')).toBeNull()
})
