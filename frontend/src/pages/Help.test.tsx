import { i18n } from '@lingui/core'
import { screen, within } from '@testing-library/react'
import { MemoryRouter } from 'react-router'
import { messages as jaMessages } from '../locales/ja.po'
import { renderWithI18n } from '../test/i18n'
import Help from './Help'

function renderHelp() {
  return renderWithI18n(
    <MemoryRouter>
      <Help />
    </MemoryRouter>,
  )
}

afterEach(() => {
  // Activation is global on the singleton; every test leaves en active.
  i18n.activate('en')
})

function activateJa() {
  i18n.load('ja', jaMessages)
  i18n.activate('ja')
}

it('renders the page title and all anchored topics', () => {
  renderHelp()
  expect(screen.getByRole('heading', { name: 'Help' })).toBeInTheDocument()
  const topics: Array<[string, string]> = [
    ['Shelves from tags', 'shelves-from-tags'],
    ['Who can see your shelves', 'visibility'],
    ['Currency display', 'currencies'],
    ['Adding games and hardware', 'adding'],
    ['Where market prices come from', 'prices'],
  ]
  for (const [name, id] of topics) {
    expect(screen.getByRole('heading', { name })).toHaveAttribute('id', id)
  }
})

it('walks through the tag-to-shelf flow with the real UI labels', () => {
  renderHelp()
  expect(screen.getByText(/Bulk edit/)).toBeInTheDocument()
  expect(screen.getByText(/Tags \(all of\)/)).toBeInTheDocument()
  expect(screen.getByText(/Save shelf\.\.\./)).toBeInTheDocument()
  expect(screen.getByText(/anyone signed in who has your link/)).toBeInTheDocument()
})

it('shows no translation notice under en', () => {
  renderHelp()
  expect(screen.queryByRole('complementary')).toBeNull()
})

it('serves the Japanese page with the controlling-text notice under ja', () => {
  activateJa()
  renderHelp()
  expect(screen.getByRole('heading', { name: 'ヘルプ' })).toBeInTheDocument()
  const aside = screen.getByRole('complementary', { name: 'この翻訳について' })
  expect(
    within(aside).getByText('この翻訳は参考のために提供されています。正文は英語版です。'),
  ).toBeInTheDocument()
})
