import { screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { renderWithI18n } from '../../test/i18n'
import DismissibleNotice from './DismissibleNotice'

it('renders the green tone with its exact classes', () => {
  renderWithI18n(
    <DismissibleNotice tone="green" dismissLabel="Dismiss thing" onDismiss={() => {}}>
      Approved.
    </DismissibleNotice>,
  )
  expect(screen.getByRole('status').className).toBe(
    'mb-4 flex items-start justify-between gap-3 rounded bg-green-50 p-3 text-sm text-green-800',
  )
  expect(screen.getByRole('button', { name: 'Dismiss thing' }).className).toBe(
    'shrink-0 rounded border border-green-300 px-2 py-0.5 hover:bg-white',
  )
  expect(screen.getByText('Approved.')).toBeInTheDocument()
})

it('renders the amber tone with its exact classes', () => {
  renderWithI18n(
    <DismissibleNotice tone="amber" dismissLabel="Dismiss other thing" onDismiss={() => {}}>
      Mismatched.
    </DismissibleNotice>,
  )
  expect(screen.getByRole('status').className).toBe(
    'mb-4 flex items-start justify-between gap-3 rounded bg-amber-50 p-3 text-sm text-amber-800',
  )
  expect(screen.getByRole('button', { name: 'Dismiss other thing' }).className).toBe(
    'shrink-0 rounded border border-amber-300 px-2 py-0.5 hover:bg-white',
  )
})

it('calls onDismiss when the button is clicked', async () => {
  const onDismiss = vi.fn()
  renderWithI18n(
    <DismissibleNotice tone="green" dismissLabel="Dismiss thing" onDismiss={onDismiss}>
      Approved.
    </DismissibleNotice>,
  )
  await userEvent.click(screen.getByRole('button', { name: 'Dismiss thing' }))
  expect(onDismiss).toHaveBeenCalledTimes(1)
})
