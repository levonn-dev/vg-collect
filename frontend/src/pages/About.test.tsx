import { i18n } from '@lingui/core'
import { cleanup, screen, within } from '@testing-library/react'
import { MemoryRouter } from 'react-router'
import { messages as jaMessages } from '../locales/ja.po'
import { renderWithI18n } from '../test/i18n'
import About from './About'
import AboutEn from './about/About.en'

function renderAbout() {
  return renderWithI18n(
    <MemoryRouter>
      <About />
    </MemoryRouter>,
  )
}

afterEach(() => {
  vi.unstubAllEnvs()
  // Unmount before touching the singleton: re-activating a mounted
  // tree is an update outside act. Leave en active after.
  cleanup()
  i18n.activate('en')
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

function activateJa() {
  i18n.load('ja', jaMessages)
  i18n.activate('ja')
}

it('shows no translation notice under en', () => {
  renderAbout()
  expect(screen.queryByRole('complementary')).toBeNull()
})

it('serves the Japanese page with the controlling-text notice under ja', () => {
  vi.stubEnv('VITE_SITE_DATA_SOURCES', 'igdb')
  vi.stubEnv('VITE_SITE_CONTACT', 'sam@example.test')
  activateJa()
  renderAbout()
  expect(screen.getByRole('heading', { name: 'vgkeepについて' })).toBeInTheDocument()
  const aside = screen.getByRole('complementary', { name: 'この翻訳について' })
  expect(
    within(aside).getByText('この翻訳は参考のために提供されています。正文は英語版です。'),
  ).toBeInTheDocument()
  expect(screen.getByText(/連絡先/)).toBeInTheDocument()
  expect(screen.getByRole('link', { name: 'sam@example.test' })).toHaveAttribute(
    'href',
    'mailto:sam@example.test',
  )
  // Same singleton i18n._() read as About.en: the data-source type
  // labels resolve from the active (ja) catalog.
  const region = screen.getByRole('region', { name: 'データソース' })
  expect(within(region).getByRole('link', { name: 'IGDB' })).toBeInTheDocument()
  expect(within(region).getByText(/ゲームデータ/)).toBeInTheDocument()
})

it('keeps the English page safe to render while a non-en locale is active', () => {
  vi.stubEnv('VITE_SITE_DATA_SOURCES', 'igdb')
  activateJa()
  renderWithI18n(<AboutEn />)
  expect(screen.getByRole('heading', { name: 'About vgkeep' })).toBeInTheDocument()
  // Guards the i18n._() label read under a non-en active locale:
  // labels resolve from the active (ja) catalog, the accepted mixed
  // render for an English fallback page.
  const region = screen.getByRole('region', { name: 'Data sources' })
  expect(within(region).getByText(/ゲームデータ/)).toBeInTheDocument()
})
