import { i18n } from '@lingui/core'
import { cleanup, screen, within } from '@testing-library/react'
import { MemoryRouter } from 'react-router'
import { messages as jaMessages } from '../locales/ja.po'
import { renderWithI18n } from '../test/i18n'
import Privacy from './Privacy'

function renderPrivacy() {
  return renderWithI18n(
    <MemoryRouter>
      <Privacy />
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

function activateJa() {
  i18n.load('ja', jaMessages)
  i18n.activate('ja')
}

it('renders the heading, cookie name, and avatar-link wording', () => {
  renderPrivacy()
  expect(screen.getByRole('heading', { name: 'Privacy policy' })).toBeInTheDocument()
  expect(screen.getByText(/__Host-vg_session/)).toBeInTheDocument()
  expect(screen.getByText(/a link to your avatar image/)).toBeInTheDocument()
})

it('mentions the IGDB image CDN only when igdb is active', () => {
  const first = renderPrivacy()
  expect(screen.queryByText(/IGDB's servers/)).toBeNull()
  first.unmount()
  vi.stubEnv('VITE_SITE_DATA_SOURCES', 'igdb,pricecharting')
  renderPrivacy()
  expect(screen.getByText(/IGDB's servers/)).toBeInTheDocument()
})

it('describes what deletion removes and what remains', () => {
  renderPrivacy()
  expect(screen.getByText(/Deleted user/)).toBeInTheDocument()
  expect(screen.getByText(/stay in the shared catalog/)).toBeInTheDocument()
})

it('names providers in the identity sentence and falls back without them', () => {
  const first = renderPrivacy()
  expect(screen.getByText(/sign in with your sign-in provider/)).toBeInTheDocument()
  first.unmount()
  vi.stubEnv('VITE_SITE_AUTH_PROVIDERS', 'google,twitch')
  renderPrivacy()
  expect(screen.getByText(/sign in with Google or Twitch/)).toBeInTheDocument()
})

it('routes data questions to the contact when set and to the operator otherwise', () => {
  const first = renderPrivacy()
  expect(screen.getByText(/go to the operator of this instance/)).toBeInTheDocument()
  first.unmount()
  vi.stubEnv('VITE_SITE_CONTACT', 'sam@example.test')
  renderPrivacy()
  expect(screen.getByText(/go to sam@example.test/)).toBeInTheDocument()
})

it('shows no translation notice under en', () => {
  renderPrivacy()
  expect(screen.queryByRole('complementary')).toBeNull()
})

it('serves the Japanese page with the controlling-text notice under ja', () => {
  vi.stubEnv('VITE_SITE_DATA_SOURCES', 'igdb')
  vi.stubEnv('VITE_SITE_AUTH_PROVIDERS', 'google,twitch')
  activateJa()
  renderPrivacy()
  expect(screen.getByRole('heading', { name: 'プライバシーポリシー' })).toBeInTheDocument()
  const aside = screen.getByRole('complementary', { name: 'この翻訳について' })
  expect(
    within(aside).getByText('この翻訳は参考のために提供されています。正文は英語版です。'),
  ).toBeInTheDocument()
  expect(screen.getByText(/カバーアート/)).toBeInTheDocument()
  expect(screen.getByText(/GoogleまたはTwitch/)).toBeInTheDocument()
})
