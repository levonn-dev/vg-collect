import { screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { renderWithI18n } from '../../test/i18n'
import VisibilityControl from './VisibilityControl'

it('marks the active segment aria-pressed and leaves the others unpressed', () => {
  renderWithI18n(<VisibilityControl value="unlisted" onChange={vi.fn()} />)
  expect(screen.getByRole('button', { name: 'Private', pressed: false })).toBeInTheDocument()
  expect(screen.getByRole('button', { name: 'Unlisted', pressed: true })).toBeInTheDocument()
  expect(screen.getByRole('button', { name: 'Listed', pressed: false })).toBeInTheDocument()
})

it('fires onChange with the clicked segment value', async () => {
  const onChange = vi.fn()
  renderWithI18n(<VisibilityControl value="unlisted" onChange={onChange} />)
  await userEvent.click(screen.getByRole('button', { name: 'Private' }))
  expect(onChange).toHaveBeenCalledWith('private')
  await userEvent.click(screen.getByRole('button', { name: 'Listed' }))
  expect(onChange).toHaveBeenCalledWith('listed')
  expect(onChange).toHaveBeenCalledTimes(2)
})

it('disables every segment when disabled', () => {
  renderWithI18n(<VisibilityControl value="private" onChange={vi.fn()} disabled />)
  expect(screen.getByRole('button', { name: 'Private' })).toBeDisabled()
  expect(screen.getByRole('button', { name: 'Unlisted' })).toBeDisabled()
  expect(screen.getByRole('button', { name: 'Listed' })).toBeDisabled()
})
