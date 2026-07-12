import { screen } from '@testing-library/react'
import { MemoryRouter } from 'react-router'
import { entryFixture } from '../../test/fixtures'
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
