import { screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { renderWithI18n } from '../test/i18n'
import LoadMoreButton from './LoadMoreButton'

function query(overrides: Partial<{ hasNextPage: boolean; isFetchingNextPage: boolean }> = {}) {
  return {
    hasNextPage: true,
    isFetchingNextPage: false,
    fetchNextPage: vi.fn(),
    ...overrides,
  }
}

it('renders nothing when there is no next page', () => {
  const q = query({ hasNextPage: false })
  const { container } = renderWithI18n(<LoadMoreButton query={q} className="mt-4" />)
  expect(container).toBeEmptyDOMElement()
})

it('renders the button with the caller-supplied margin ahead of the shared classes', () => {
  const q = query()
  renderWithI18n(<LoadMoreButton query={q} className="mt-2" />)
  expect(screen.getByRole('button', { name: 'Load more' }).className).toBe(
    'mt-2 rounded border border-gray-300 px-3 py-1 text-sm hover:bg-gray-50 disabled:opacity-50',
  )
})

it('fetches the next page on click', async () => {
  const q = query()
  renderWithI18n(<LoadMoreButton query={q} className="mt-4" />)
  await userEvent.click(screen.getByRole('button', { name: 'Load more' }))
  expect(q.fetchNextPage).toHaveBeenCalledTimes(1)
})

it('disables the button while a fetch is already in flight', () => {
  const q = query({ isFetchingNextPage: true })
  renderWithI18n(<LoadMoreButton query={q} className="mt-4" />)
  expect(screen.getByRole('button', { name: 'Load more' })).toBeDisabled()
})
