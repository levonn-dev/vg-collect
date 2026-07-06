import { render, screen } from '@testing-library/react'
import ValueOverTime from './ValueOverTime'

it('explains an empty series', () => {
  render(<ValueOverTime history={{ available: true, points: [] }} />)
  expect(screen.getByText(/appears once price snapshots accumulate/i)).toBeInTheDocument()
})

it('flags a degraded read', () => {
  render(<ValueOverTime history={{ available: false, points: [] }} />)
  expect(screen.getByRole('alert')).toHaveTextContent(/temporarily unavailable/i)
})

it('renders the chart region when points exist', () => {
  render(
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
})
