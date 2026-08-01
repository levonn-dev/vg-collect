import { screen } from '@testing-library/react'
import { MemoryRouter } from 'react-router'
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
})

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
