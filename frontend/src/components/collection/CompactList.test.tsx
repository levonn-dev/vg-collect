import { screen } from '@testing-library/react'
import { MemoryRouter } from 'react-router'
import { entryFixture } from '../../test/fixtures'
import { renderWithMoney } from '../../test/money'
import CompactList from './CompactList'

it('renders values converted into the display currency', () => {
  renderWithMoney(
    <MemoryRouter>
      <CompactList entries={[entryFixture({ value_cents: 4200 })]} />
    </MemoryRouter>,
    { currency: 'EUR' },
  )
  expect(screen.getByText('€21.00')).toBeInTheDocument()
})
