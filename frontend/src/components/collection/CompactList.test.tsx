import { i18n } from '@lingui/core'
import { cleanup, screen } from '@testing-library/react'
import { MemoryRouter } from 'react-router'
import type { Entry } from '../../api/collection'
import { messages as jaMessages } from '../../locales/ja.po'
import { entryFixture, sharedEntryFixture } from '../../test/fixtures'
import { renderWithMoney } from '../../test/money'
import CompactList from './CompactList'

afterEach(() => {
  // cleanup() before activate(): otherwise I18nProvider updates outside act.
  cleanup()
  i18n.activate('en')
})

function activateJa() {
  i18n.load('ja', jaMessages)
  i18n.activate('ja')
}

// JP-trio fixture: see EntryTable.test.tsx / productTitle.test.ts.
const jp: Partial<Entry> = {
  display_name: 'Trials of Mana',
  localized_name: '聖剣伝説 3',
  localized_name_translit: 'Seiken Densetsu 3',
  localized_cover_url: 'https://x/jp.jpg',
  region: 'ntsc_j',
}

it('renders values converted into the display currency', () => {
  renderWithMoney(
    <MemoryRouter>
      <CompactList entries={[entryFixture({ value_cents: 4200 })]} />
    </MemoryRouter>,
    { currency: 'EUR' },
  )
  expect(screen.getByText('€21.00')).toBeInTheDocument()
})

it('suppresses the name link and renders plain text when linkTo returns null', () => {
  renderWithMoney(
    <MemoryRouter>
      <CompactList entries={[entryFixture({ display_name: 'Chrono Trigger' })]} linkTo={() => null} />
    </MemoryRouter>,
  )
  expect(screen.getByText('Chrono Trigger')).toBeInTheDocument()
  expect(screen.queryByRole('link', { name: /Chrono Trigger/ })).not.toBeInTheDocument()
})

it('omits the status badge for a SharedEntry row instead of crashing, and still shows platform/value dashes', () => {
  renderWithMoney(
    <MemoryRouter>
      <CompactList
        entries={[sharedEntryFixture({ display_name: 'Someone Elses Game', platform: undefined })]}
        linkTo={() => null}
      />
    </MemoryRouter>,
  )
  expect(screen.getByText('Someone Elses Game')).toBeInTheDocument()
  expect(screen.queryByText('Backlog')).not.toBeInTheDocument()
  expect(screen.getAllByText('-').length).toBeGreaterThan(0)
})

it('omits the trailing value span when shared, leaving no dash in its place', () => {
  renderWithMoney(
    <MemoryRouter>
      <CompactList
        entries={[sharedEntryFixture({ display_name: 'Someone Elses Game' })]}
        linkTo={() => null}
        shared
      />
    </MemoryRouter>,
  )
  expect(screen.getByText('Someone Elses Game')).toBeInTheDocument()
  expect(screen.getByText('SNES')).toBeInTheDocument()
  expect(screen.queryByText('-')).not.toBeInTheDocument()
})

it('renders the romanized title with a ja-Latn lang attribute by default', () => {
  renderWithMoney(
    <MemoryRouter>
      <CompactList entries={[entryFixture(jp)]} />
    </MemoryRouter>,
  )
  expect(screen.getByText('Seiken Densetsu 3')).toHaveAttribute('lang', 'ja-Latn')
})

it('renders the native title with a ja lang attribute under the ja locale', () => {
  activateJa()
  renderWithMoney(
    <MemoryRouter>
      <CompactList entries={[entryFixture(jp)]} />
    </MemoryRouter>,
  )
  expect(screen.getByText('聖剣伝説 3')).toHaveAttribute('lang', 'ja')
})

it('leaves the lang attribute off a canonical-only title', () => {
  renderWithMoney(
    <MemoryRouter>
      <CompactList entries={[entryFixture({ display_name: 'Chrono Trigger' })]} />
    </MemoryRouter>,
  )
  expect(screen.getByText('Chrono Trigger')).not.toHaveAttribute('lang')
})
