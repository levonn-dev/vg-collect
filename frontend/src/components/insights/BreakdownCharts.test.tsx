import { screen, within } from '@testing-library/react'
import { dashboardFixture } from '../../test/fixtures'
import { renderWithI18n } from '../../test/i18n'
import BreakdownCharts from './BreakdownCharts'

it('shows "Nothing yet" in a count list with no rows', () => {
  renderWithI18n(<BreakdownCharts dashboard={dashboardFixture({ by_status: {} })} />)
  expect(within(screen.getByRole('region', { name: 'By status' })).getByText('Nothing yet')).toBeInTheDocument()
  // The other list still gets its ordinary rows.
  expect(within(screen.getByRole('region', { name: 'By item type' })).queryByText('Nothing yet')).not.toBeInTheDocument()
})
