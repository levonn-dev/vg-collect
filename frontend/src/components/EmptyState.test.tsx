import { screen } from '@testing-library/react'
import { renderWithI18n } from '../test/i18n'
import EmptyState from './EmptyState'

it('renders the default size at py-12', () => {
  renderWithI18n(<EmptyState size="default">Nothing here.</EmptyState>)
  expect(screen.getByText('Nothing here.').className).toBe('py-12 text-center text-gray-500')
})

it('renders the compact size at py-8', () => {
  renderWithI18n(<EmptyState size="compact">This shelf is empty.</EmptyState>)
  expect(screen.getByText('This shelf is empty.').className).toBe('py-8 text-center text-gray-500')
})

it('renders arbitrary children, not just plain text', () => {
  renderWithI18n(
    <EmptyState size="default">
      Nothing yet. <a href="/explore">Explore</a> people to follow.
    </EmptyState>,
  )
  expect(screen.getByRole('link', { name: 'Explore' })).toHaveAttribute('href', '/explore')
})
