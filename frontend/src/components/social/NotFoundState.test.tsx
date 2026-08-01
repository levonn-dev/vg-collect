import { screen } from '@testing-library/react'
import { renderWithI18n } from '../../test/i18n'
import NotFoundState from './NotFoundState'

it('renders the shared "nothing here" copy as an alert', () => {
  renderWithI18n(<NotFoundState />)
  expect(screen.getByRole('alert')).toHaveTextContent('Nothing here.')
})
