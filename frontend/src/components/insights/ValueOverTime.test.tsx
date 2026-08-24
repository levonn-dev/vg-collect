import { screen, within } from '@testing-library/react'
import { renderWithMoney } from '../../test/money'
import ValueOverTime from './ValueOverTime'

it('explains an empty series', () => {
  renderWithMoney(<ValueOverTime history={{ available: true, points: [] }} />)
  expect(screen.getByText(/appears when price snapshots accumulate/i)).toBeInTheDocument()
})

it('flags a degraded read', () => {
  renderWithMoney(<ValueOverTime history={{ available: false, points: [] }} />)
  expect(screen.getByRole('alert')).toHaveTextContent(/temporarily unavailable/i)
})

it('renders the chart region with the whole-collection caption, titled in USD', () => {
  renderWithMoney(
    <ValueOverTime
      history={{
        available: true,
        points: [
          { date: '2026-07-01', value_cents: 4200 },
          { date: '2026-07-02', value_cents: 5700 },
        ],
      }}
    />,
  )
  expect(screen.getByRole('region', { name: 'Collection value over time' })).toBeInTheDocument()
  // Scoped to the heading role: the sr-only data table's own <caption>
  // repeats this exact sentence for non-visual users (see the test
  // below), so a plain getByText would no longer resolve uniquely.
  expect(screen.getByRole('heading', { name: 'Collection value in USD (last 90 days)' })).toBeInTheDocument()
  // The series never follows filters (snapshots are aggregate
  // history); the caption must say so.
  expect(screen.getByText(/covers your whole collection/i)).toBeInTheDocument()
})

it('titles the chart in the display currency', () => {
  renderWithMoney(
    <ValueOverTime
      history={{
        available: true,
        points: [{ date: '2026-07-01', value_cents: 4200 }],
      }}
    />,
    { currency: 'EUR' },
  )
  expect(screen.getByRole('heading', { name: 'Collection value in EUR (last 90 days)' })).toBeInTheDocument()
})

it('gives the chart a visually-hidden data table covering the same points', () => {
  renderWithMoney(
    <ValueOverTime
      history={{
        available: true,
        points: [
          { date: '2026-07-01', value_cents: 4200 },
          { date: '2026-07-02', value_cents: 5700 },
        ],
      }}
    />,
  )
  const table = screen.getByRole('table')
  expect(within(table).getByText('Collection value in USD (last 90 days)')).toBeInTheDocument()
  const rows = within(table).getAllByRole('row').slice(1) // drop the header row
  expect(within(rows[0]).getByRole('cell', { name: '2026-07-01' })).toBeInTheDocument()
  expect(within(rows[0]).getByRole('cell', { name: '$42.00' })).toBeInTheDocument()
  expect(within(rows[1]).getByRole('cell', { name: '2026-07-02' })).toBeInTheDocument()
  expect(within(rows[1]).getByRole('cell', { name: '$57.00' })).toBeInTheDocument()
})
