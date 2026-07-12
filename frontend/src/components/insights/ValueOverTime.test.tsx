import { screen } from '@testing-library/react'
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
  expect(screen.getByText('Collection value in USD (last 90 days)')).toBeInTheDocument()
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
  expect(screen.getByText('Collection value in EUR (last 90 days)')).toBeInTheDocument()
})
