import { fireEvent, render, screen } from '@testing-library/react'
import { MemoryRouter } from 'react-router'
import type { ProfileCard } from '../../api/social'
import UserChip from './UserChip'

const profile: ProfileCard = {
  user_id: 'u1', handle: 'Alice_Prime', profile_visibility: 'listed',
}

function renderChip(p: ProfileCard = profile) {
  return render(
    <MemoryRouter>
      <UserChip profile={p} />
    </MemoryRouter>,
  )
}

it('renders @handle linking to the profile route', () => {
  renderChip()
  const link = screen.getByRole('link', { name: '@Alice_Prime' })
  expect(link).toHaveAttribute('href', '/u/Alice_Prime')
})

it('shows an initial fallback when there is no avatar', () => {
  renderChip()
  expect(document.querySelector('img')).toBeNull()
  expect(screen.getByText('A', { selector: 'span[aria-hidden="true"]' })).toBeInTheDocument()
})

it('renders the avatar image without a referrer when present', () => {
  renderChip({ ...profile, avatar_url: 'https://img.example/a.png' })
  const img = document.querySelector('img')
  expect(img).toHaveAttribute('src', 'https://img.example/a.png')
  expect(img).toHaveAttribute('referrerpolicy', 'no-referrer')
  expect(img).toHaveAttribute('alt', '')
})

it('falls back to the initial once the image fails to load', () => {
  renderChip({ ...profile, avatar_url: 'https://img.example/a.png' })
  fireEvent.error(document.querySelector('img')!)
  expect(document.querySelector('img')).toBeNull()
  expect(screen.getByText('A', { selector: 'span[aria-hidden="true"]' })).toBeInTheDocument()
})
