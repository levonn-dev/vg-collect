import { render, screen } from '@testing-library/react'
import NotFoundState from './NotFoundState'

it('renders the shared "nothing here" copy as an alert', () => {
  render(<NotFoundState />)
  expect(screen.getByRole('alert')).toHaveTextContent('Nothing here.')
})
