import { screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { renderWithI18n } from '../test/i18n'
import SearchPickerDialog from './SearchPickerDialog'

function renderDialog(onClose = vi.fn()) {
  return renderWithI18n(
    <SearchPickerDialog ariaLabel="Pick a thing" title="Pick a thing" onClose={onClose}>
      <input type="text" aria-label="query" />
      <p>child content</p>
    </SearchPickerDialog>,
  )
}

it('renders a modal dialog with the given aria-label and title', () => {
  renderDialog()
  const dialog = screen.getByRole('dialog', { name: 'Pick a thing' })
  expect(dialog).toHaveAttribute('aria-modal', 'true')
  expect(screen.getByText('Pick a thing')).toBeInTheDocument()
})

it('renders the caller-supplied children', () => {
  renderDialog()
  expect(screen.getByText('child content')).toBeInTheDocument()
  expect(screen.getByRole('textbox', { name: 'query' })).toBeInTheDocument()
})

it('moves focus to the first input on mount and returns it to the opener on unmount', () => {
  const opener = document.createElement('button')
  document.body.appendChild(opener)
  opener.focus()
  const { unmount } = renderDialog()
  expect(document.activeElement).toBe(screen.getByRole('textbox', { name: 'query' }))
  unmount()
  expect(document.activeElement).toBe(opener)
  opener.remove()
})

it('calls onClose when the Close button is clicked', async () => {
  const onClose = vi.fn()
  renderDialog(onClose)
  await userEvent.click(screen.getByRole('button', { name: 'Close' }))
  expect(onClose).toHaveBeenCalledTimes(1)
})
