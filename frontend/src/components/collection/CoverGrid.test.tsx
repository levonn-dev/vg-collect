import { i18n } from '@lingui/core'
import { cleanup, screen } from '@testing-library/react'
import { MemoryRouter } from 'react-router'
import type { Entry } from '../../api/collection'
import { messages as jaMessages } from '../../locales/ja.po'
import { entryFixture, sharedEntryFixture } from '../../test/fixtures'
import { renderWithMoney } from '../../test/money'
import CoverGrid from './CoverGrid'

const renderGrid = (entries = [entryFixture()], opts: { currency?: string } = {}) =>
  renderWithMoney(
    <MemoryRouter>
      <CoverGrid entries={entries} />
    </MemoryRouter>,
    opts,
  )

afterEach(() => {
  // Order matters: cleanup() before activate() - see EntryTable.test.tsx's
  // afterEach for why (I18nProvider update outside act otherwise).
  cleanup()
  i18n.activate('en')
})

function activateJa() {
  i18n.load('ja', jaMessages)
  i18n.activate('ja')
}

// JP-trio fixture (see EntryTable.test.tsx / productTitle.test.ts);
// cover_url is also set so the localized-cover assertion proves
// precedence, not absence.
const jp: Partial<Entry> = {
  display_name: 'Trials of Mana',
  localized_name: '聖剣伝説 3',
  localized_name_translit: 'Seiken Densetsu 3',
  localized_cover_url: 'https://x/jp.jpg',
  cover_url: 'https://x/na.jpg',
  region: 'ntsc_j',
}

it('renders cover art when the snapshot exists', () => {
  renderGrid([entryFixture({ display_name: 'Chrono Trigger', cover_url: 'https://img.example/ct.jpg' })])
  const img = document.querySelector('img')
  expect(img).toHaveAttribute('src', 'https://img.example/ct.jpg')
  expect(img).toHaveAttribute('alt', '') // decorative: the card names the entry
  expect(screen.getByRole('link', { name: /Chrono Trigger/ })).toBeInTheDocument()
})

it('falls back to a type icon without cover art', () => {
  renderGrid([entryFixture({ display_name: 'Repro Cart', item_type: 'accessory', cover_url: undefined })])
  expect(document.querySelector('img')).toBeNull()
  expect(document.querySelector('svg[data-icon="accessory"]')).toBeInTheDocument()
})

it('contains hardware images instead of cropping them', () => {
  renderGrid([
    entryFixture({ display_name: 'Gamecube System', item_type: 'console', cover_url: 'https://img.example/logo.jpg' }),
  ])
  expect(document.querySelector('img')).toHaveClass('object-contain')
})

it('shows value and pin state', () => {
  renderGrid([entryFixture({ value_cents: 4200, pinned: true })])
  expect(screen.getByText('$42.00')).toBeInTheDocument()
  expect(screen.getByLabelText('Pinned')).toBeInTheDocument()
})

it('converts the value into the display currency', () => {
  renderGrid([entryFixture({ value_cents: 4200 })], { currency: 'EUR' })
  expect(screen.getByText('€21.00')).toBeInTheDocument()
})

it('suppresses the card link and renders plain content when linkTo returns null', () => {
  renderWithMoney(
    <MemoryRouter>
      <CoverGrid entries={[entryFixture({ display_name: 'Chrono Trigger' })]} linkTo={() => null} />
    </MemoryRouter>,
  )
  expect(screen.getByText('Chrono Trigger')).toBeInTheDocument()
  expect(screen.queryByRole('link', { name: /Chrono Trigger/ })).not.toBeInTheDocument()
})

it('renders a SharedEntry card read-only (no link, no fabricated value) without crashing', () => {
  renderWithMoney(
    <MemoryRouter>
      <CoverGrid
        entries={[sharedEntryFixture({ display_name: 'Someone Elses Game', cover_url: undefined })]}
        linkTo={() => null}
      />
    </MemoryRouter>,
  )
  expect(screen.getByText('Someone Elses Game')).toBeInTheDocument()
  expect(screen.queryByRole('link')).not.toBeInTheDocument()
  expect(document.querySelector('svg[data-icon="game"]')).toBeInTheDocument()
  // The actual "no fabricated value" check: a SharedEntry carries no
  // price fields, so rowMeta falls back to '-' rather than calling
  // money.entryValue on a row that has nothing for it to format - a
  // full Entry row with a price renders a $-prefixed string (see
  // "shows value and pin state" above), so a $ anywhere here would be
  // a fabricated figure.
  expect(screen.queryByText(/\$/)).not.toBeInTheDocument()
})

it('omits the value line when shared, keeping the platform label', () => {
  renderWithMoney(
    <MemoryRouter>
      <CoverGrid
        entries={[sharedEntryFixture({ display_name: 'Someone Elses Game', cover_url: undefined })]}
        linkTo={() => null}
        shared
      />
    </MemoryRouter>,
  )
  expect(screen.getByText('Someone Elses Game')).toBeInTheDocument()
  expect(screen.getByText('SNES')).toBeInTheDocument()
  expect(screen.queryByText('-')).not.toBeInTheDocument()
})

it('renders the romanized title, ja-Latn lang, and the localized cover by default', () => {
  renderGrid([entryFixture(jp)])
  expect(screen.getByText('Seiken Densetsu 3')).toHaveAttribute('lang', 'ja-Latn')
  expect(document.querySelector('img')).toHaveAttribute('src', 'https://x/jp.jpg')
})

it('renders the native title with a ja lang attribute under the ja locale', () => {
  activateJa()
  renderGrid([entryFixture(jp)])
  expect(screen.getByText('聖剣伝説 3')).toHaveAttribute('lang', 'ja')
})

it('leaves the lang attribute off a canonical-only tile title', () => {
  renderGrid([entryFixture({ display_name: 'Chrono Trigger' })])
  expect(screen.getByText('Chrono Trigger')).not.toHaveAttribute('lang')
})
