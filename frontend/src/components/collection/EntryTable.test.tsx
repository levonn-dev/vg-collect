import { screen } from '@testing-library/react'
import { MemoryRouter } from 'react-router'
import type { Entry } from '../../api/collection'
import { entryFixture } from '../../test/fixtures'
import { renderWithMoney } from '../../test/money'
import EntryTable from './EntryTable'

const renderTable = (entries: Entry[], opts: { currency?: string } = {}) =>
  renderWithMoney(
    <MemoryRouter>
      <EntryTable entries={entries} />
    </MemoryRouter>,
    opts,
  )

it('renders values converted into the display currency, header labeled', () => {
  renderTable([entryFixture({ value_cents: 4200 })], { currency: 'EUR' })
  expect(screen.getByText('Value (EUR)')).toBeInTheDocument()
  expect(screen.getByText('€21.00')).toBeInTheDocument()
})

it('pins a matching entered pair instead of converting the snapshot', () => {
  renderTable(
    [
      entryFixture({
        pricing_mode: 'custom',
        value_cents: 11900,
        custom_value_cents: 11900,
        custom_value_entered_cents: 6000,
        custom_value_entered_currency: 'EUR',
      }),
    ],
    { currency: 'EUR' },
  )
  expect(screen.getByText('€60.00')).toBeInTheDocument()
})

it('leaves the paid column in its stored currency', () => {
  renderTable([entryFixture({ price_paid_cents: 5000, currency: 'JPY' })], { currency: 'EUR' })
  expect(screen.getByText('¥50')).toBeInTheDocument()
})
