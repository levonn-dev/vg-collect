import { screen } from '@testing-library/react'
import { renderWithI18n } from '../../test/i18n'
import Pager from './Pager'

it('collapses to a count line when one page suffices', () => {
  renderWithI18n(<Pager page={0} totalCount={12} onPage={() => {}} />)
  expect(screen.getByText('12 items')).toBeInTheDocument()
  expect(screen.queryByRole('button')).not.toBeInTheDocument()
})

it('bounds the buttons and reports the window', () => {
  renderWithI18n(<Pager page={1} totalCount={450} onPage={() => {}} />)
  expect(screen.getByText('201-400 of 450')).toBeInTheDocument()
  expect(screen.getByRole('button', { name: 'Previous' })).toBeEnabled()
  expect(screen.getByRole('button', { name: 'Next' })).toBeEnabled()
})

it('disables past the last page', () => {
  renderWithI18n(<Pager page={2} totalCount={450} onPage={() => {}} />)
  expect(screen.getByRole('button', { name: 'Next' })).toBeDisabled()
})
