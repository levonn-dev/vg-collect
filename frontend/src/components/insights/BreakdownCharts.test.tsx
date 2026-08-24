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

it('shows "Nothing yet" for the By platform chart too, when there are no platforms', () => {
  renderWithI18n(<BreakdownCharts dashboard={dashboardFixture({ by_platform: [] })} />)
  expect(within(screen.getByRole('region', { name: 'By platform' })).getByText('Nothing yet')).toBeInTheDocument()
})

it('gives the By platform chart a visually-hidden data table covering the same points', () => {
  renderWithI18n(<BreakdownCharts dashboard={dashboardFixture()} />)
  const table = within(screen.getByRole('region', { name: 'By platform' })).getByRole('table')
  const rows = within(table).getAllByRole('row').slice(1) // drop the header row
  expect(within(rows[0]).getByRole('cell', { name: 'SNES' })).toBeInTheDocument()
  expect(within(rows[0]).getByRole('cell', { name: '21' })).toBeInTheDocument()
  expect(within(rows[1]).getByRole('cell', { name: 'PlayStation' })).toBeInTheDocument()
  expect(within(rows[1]).getByRole('cell', { name: '14' })).toBeInTheDocument()
})
