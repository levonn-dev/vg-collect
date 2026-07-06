import { render, screen } from '@testing-library/react'
import { MemoryRouter } from 'react-router'
import { entryFixture } from '../../test/fixtures'
import CoverGrid from './CoverGrid'

const renderGrid = (entries = [entryFixture()]) =>
  render(
    <MemoryRouter>
      <CoverGrid entries={entries} />
    </MemoryRouter>,
  )

it('renders cover art when the snapshot exists', () => {
  renderGrid([entryFixture({ display_name: 'Chrono Trigger', cover_url: 'https://img.example/ct.jpg' })])
  const img = document.querySelector('img')
  expect(img).toHaveAttribute('src', 'https://img.example/ct.jpg')
  expect(img).toHaveAttribute('alt', '') // decorative: the card names the entry
  expect(screen.getByRole('link', { name: /Chrono Trigger/ })).toBeInTheDocument()
})

it('falls back to an initial-letter tile', () => {
  renderGrid([entryFixture({ display_name: 'Repro Cart', cover_url: undefined })])
  expect(document.querySelector('img')).toBeNull()
  expect(screen.getByText('R')).toBeInTheDocument()
})

it('shows value and pin state', () => {
  renderGrid([entryFixture({ value_cents: 4200, pinned: true })])
  expect(screen.getByText('$42.00')).toBeInTheDocument()
  expect(screen.getByLabelText('Pinned')).toBeInTheDocument()
})
