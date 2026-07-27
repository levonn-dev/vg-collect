import { screen } from '@testing-library/react'
import { MemoryRouter } from 'react-router'
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
