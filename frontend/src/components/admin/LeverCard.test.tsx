import { screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { renderWithI18n } from '../../test/i18n'
import LeverCard from './LeverCard'

const base = {
  title: 'Sweep things',
  actionLabel: 'Run the sweep',
  onRun: () => {},
  pending: false,
}

it('renders the labeled card and fires the action', async () => {
  const onRun = vi.fn()
  renderWithI18n(<LeverCard {...base} onRun={onRun} />)
  expect(screen.getByRole('region', { name: 'Sweep things' })).toBeInTheDocument()
  expect(screen.getByRole('heading', { name: 'Sweep things', level: 4 })).toBeInTheDocument()
  await userEvent.click(screen.getByRole('button', { name: 'Run the sweep' }))
  expect(onRun).toHaveBeenCalledTimes(1)
})

it('disables the button while pending', () => {
  renderWithI18n(<LeverCard {...base} pending />)
  expect(screen.getByRole('button', { name: 'Run the sweep' })).toBeDisabled()
})

it('renders the success line when given', () => {
  renderWithI18n(<LeverCard {...base} success="All done." />)
  expect(screen.getByText('All done.')).toBeInTheDocument()
  expect(screen.queryByRole('alert')).not.toBeInTheDocument()
})

it('renders the error as an alert when given', () => {
  renderWithI18n(<LeverCard {...base} error="It broke." />)
  expect(screen.getByRole('alert')).toHaveTextContent('It broke.')
})
