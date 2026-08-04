import { i18n } from '@lingui/core'
import { cleanup, screen, within } from '@testing-library/react'
import { MemoryRouter } from 'react-router'
import { messages as jaMessages } from '../locales/ja.po'
import { renderWithI18n } from '../test/i18n'
import Terms from './Terms'

function renderTerms() {
  return renderWithI18n(
    <MemoryRouter>
      <Terms />
    </MemoryRouter>,
  )
}

afterEach(() => {
  vi.unstubAllEnvs()
  // Unmount before touching the singleton (this hook runs ahead of
  // RTL's auto-cleanup; re-activating against a mounted tree is an
  // I18nProvider update outside act), then leave en active.
  cleanup()
  i18n.activate('en')
})

function activateJa() {
  i18n.load('ja', jaMessages)
  i18n.activate('ja')
}

it('renders the heading and a last-updated line', () => {
  renderTerms()
  expect(screen.getByRole('heading', { name: 'Terms of service' })).toBeInTheDocument()
  expect(screen.getByRole('heading', { name: 'Termination' })).toBeInTheDocument()
  expect(screen.getByText(/Last updated: 2026-07-30/)).toBeInTheDocument()
})

it('names both providers when both are active', () => {
  vi.stubEnv('VITE_SITE_AUTH_PROVIDERS', 'google,twitch')
  renderTerms()
  expect(screen.getByText(/an account with Google or Twitch/)).toBeInTheDocument()
})

it('names a single active provider alone', () => {
  vi.stubEnv('VITE_SITE_AUTH_PROVIDERS', 'twitch')
  renderTerms()
  expect(screen.getByText(/an account with Twitch/)).toBeInTheDocument()
})

it('falls back to generic phrasing with no providers', () => {
  renderTerms()
  expect(screen.getByText(/a third-party sign-in provider/)).toBeInTheDocument()
})

it('names the jurisdiction when set and falls back otherwise', () => {
  const first = renderTerms()
  expect(screen.getByText(/law of the operator's jurisdiction/)).toBeInTheDocument()
  first.unmount()
  vi.stubEnv('VITE_SITE_JURISDICTION', 'Germany')
  renderTerms()
  expect(screen.getByText(/law of Germany/)).toBeInTheDocument()
})

it('routes terms questions to the contact when set and to the operator otherwise', () => {
  const first = renderTerms()
  expect(screen.getByText(/go to the operator of this instance/)).toBeInTheDocument()
  first.unmount()
  vi.stubEnv('VITE_SITE_CONTACT', 'sam@example.test')
  renderTerms()
  expect(screen.getByText(/go to sam@example.test/)).toBeInTheDocument()
})

it('shows no translation notice under en', () => {
  renderTerms()
  expect(screen.queryByRole('complementary')).toBeNull()
})

it('serves the Japanese page with the controlling-text notice under ja', () => {
  vi.stubEnv('VITE_SITE_AUTH_PROVIDERS', 'google,twitch')
  activateJa()
  renderTerms()
  expect(screen.getByRole('heading', { name: '利用規約' })).toBeInTheDocument()
  const aside = screen.getByRole('complementary', { name: 'この翻訳について' })
  expect(
    within(aside).getByText('この翻訳は参考のために提供されています。正文は英語版です。'),
  ).toBeInTheDocument()
  expect(screen.getByText(/GoogleまたはTwitch/)).toBeInTheDocument()
})
