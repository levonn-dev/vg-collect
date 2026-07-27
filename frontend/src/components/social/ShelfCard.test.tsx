import { render, screen } from '@testing-library/react'
import { MemoryRouter } from 'react-router'
import type { ShelfCard as ShelfCardData } from '../../api/social'
import ShelfCard from './ShelfCard'

const card: ShelfCardData = {
  id: 's1', name: 'Backlog Wall', slug: 'backlog-wall',
  owner: { user_id: 'u1', handle: 'Alice_Prime', profile_visibility: 'listed' },
  published_at: '2026-07-01T00:00:00Z',
  entry_count: 12,
  cover_urls: ['https://img.example/a.jpg', 'https://img.example/b.jpg'],
  like_count: 3,
  comment_count: 1,
  viewer_likes: false,
}

function renderCard(overrides: Partial<ShelfCardData> = {}) {
  return render(
    <MemoryRouter>
      <ShelfCard card={{ ...card, ...overrides }} />
    </MemoryRouter>,
  )
}

it('links the shelf name to the owner/slug route and the byline to the profile route', () => {
  renderCard()
  expect(screen.getByRole('link', { name: 'Backlog Wall' }))
    .toHaveAttribute('href', '/u/Alice_Prime/shelves/backlog-wall')
  expect(screen.getByRole('link', { name: '@Alice_Prime' })).toHaveAttribute('href', '/u/Alice_Prime')
})

it('renders entry count, like count, and the published date when present', () => {
  renderCard()
  expect(screen.getByText('12 entries')).toBeInTheDocument()
  expect(screen.getByText('3 likes')).toBeInTheDocument()
  expect(screen.getByText(new Date(card.published_at!).toLocaleDateString())).toBeInTheDocument()
})

it('omits the like count when the social summary is absent', () => {
  renderCard({ like_count: undefined })
  expect(screen.queryByText(/\blikes?$/)).not.toBeInTheDocument()
})

it('singularizes a one-entry, one-like count', () => {
  renderCard({ entry_count: 1, like_count: 1 })
  expect(screen.getByText('1 entry')).toBeInTheDocument()
  expect(screen.getByText('1 like')).toBeInTheDocument()
})

it('renders up to four cover thumbnails and no more', () => {
  renderCard({ cover_urls: ['a', 'b', 'c', 'd', 'e'] })
  expect(document.querySelectorAll('img')).toHaveLength(4)
})

it('renders no cover strip when the shelf has no covers', () => {
  renderCard({ cover_urls: [] })
  expect(document.querySelectorAll('img')).toHaveLength(0)
})

it('applies the shared card classes', () => {
  renderCard()
  expect(screen.getByRole('link', { name: 'Backlog Wall' }).closest('div'))
    .toHaveClass('rounded', 'border', 'border-gray-200', 'p-2')
})
