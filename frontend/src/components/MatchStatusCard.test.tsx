import { screen } from '@testing-library/react'
import { renderWithMoney } from '../test/money'
import MatchStatusCard from './MatchStatusCard'

const pc = {
  pc_product_id: 55, pc_name: 'Chrono Trigger', console_name: 'Super Nintendo',
  match_confidence: 0.93, verified: true,
  loose_cents: 1500, cib_cents: 4200, new_cents: 9900, as_of: '2026-07-01T00:00:00Z',
}

it('shows the verified status line and the price triple when prices are on', () => {
  renderWithMoney(<MatchStatusCard pc={pc} showPrices />)
  expect(screen.getByText(/priced as "chrono trigger" \(super nintendo\) - match 93%, verified\./i)).toBeInTheDocument()
  expect(screen.getByText('Loose $15.00 / CIB $42.00 / New $99.00')).toBeInTheDocument()
})

it('drops the verified clause and hides the price triple when prices are off', () => {
  renderWithMoney(<MatchStatusCard pc={{ ...pc, verified: false }} showPrices={false} />)
  expect(screen.getByText(/priced as "chrono trigger" \(super nintendo\) - match 93%\./i)).toBeInTheDocument()
  expect(screen.queryByText(/verified/i)).not.toBeInTheDocument()
  expect(screen.queryByText(/loose/i)).not.toBeInTheDocument()
})

it('renders caller-supplied trailing content after the status line', () => {
  renderWithMoney(
    <MatchStatusCard pc={pc} showPrices={false}>
      <button type="button">Change listing</button>
    </MatchStatusCard>,
  )
  expect(screen.getByRole('button', { name: 'Change listing' })).toBeInTheDocument()
})
