import { screen } from '@testing-library/react'
import { dashboardFixture } from '../../test/fixtures'
import { renderWithMoney } from '../../test/money'
import StatCards from './StatCards'

it('labels and converts the collection value', () => {
  renderWithMoney(<StatCards dashboard={dashboardFixture()} />, { currency: 'EUR' })
  expect(screen.getByText('Collection value (EUR)')).toBeInTheDocument()
  expect(screen.getByText('€1,921.00')).toBeInTheDocument() // 384200 * 0.5 / 100
})

it('explains when there are no recorded purchase prices', () => {
  renderWithMoney(<StatCards dashboard={dashboardFixture({ spend: [] })} />)
  expect(screen.getByText('No purchase prices recorded.')).toBeInTheDocument()
})

it('shows the unavailable-pricing message instead of the value stats when pricing is unavailable', () => {
  renderWithMoney(<StatCards dashboard={dashboardFixture({
    pricing: { available: false, priced_entries: 0, unpriced_entries: 0, excluded_entries: 0 },
  })} />)
  expect(screen.getByRole('alert')).toHaveTextContent('Value unavailable right now; try again shortly.')
  expect(screen.queryByText('€1,921.00')).not.toBeInTheDocument()
  expect(screen.queryByText(/priced.*unpriced.*excluded/)).not.toBeInTheDocument()
})
