import { render, screen } from '@testing-library/react'
import { MemoryRouter } from 'react-router'
import Help from './Help'

function renderHelp() {
  return render(
    <MemoryRouter>
      <Help />
    </MemoryRouter>,
  )
}

it('renders the page title and all anchored topics', () => {
  renderHelp()
  expect(screen.getByRole('heading', { name: 'Help' })).toBeInTheDocument()
  const topics: Array<[string, string]> = [
    ['Shelves from tags', 'shelves-from-tags'],
    ['Who can see your shelves', 'visibility'],
    ['Currency display', 'currencies'],
    ['Adding games and hardware', 'adding'],
    ['Where market prices come from', 'prices'],
  ]
  for (const [name, id] of topics) {
    expect(screen.getByRole('heading', { name })).toHaveAttribute('id', id)
  }
})

it('walks through the tag-to-shelf flow with the real UI labels', () => {
  renderHelp()
  expect(screen.getByText(/Bulk edit/)).toBeInTheDocument()
  expect(screen.getByText(/Tags \(all of\)/)).toBeInTheDocument()
  expect(screen.getByText(/Save shelf\.\.\./)).toBeInTheDocument()
  expect(screen.getByText(/anyone signed in who has your link/)).toBeInTheDocument()
})
