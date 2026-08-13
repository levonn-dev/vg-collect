import { screen } from '@testing-library/react'
import { renderWithI18n } from '../test/i18n'
import EntryGroupSection from './EntryGroupSection'

it('renders a labeled section with the heading and the given children', () => {
  renderWithI18n(
    <EntryGroupSection label="Backlog">
      <p>row content</p>
    </EntryGroupSection>,
  )
  const section = screen.getByRole('region', { name: 'Backlog' })
  expect(section.className).toBe('mb-6')
  expect(screen.getByRole('heading', { name: 'Backlog', level: 3 })).toBeInTheDocument()
  expect(screen.getByText('row content')).toBeInTheDocument()
})

it('renders arbitrary children, not just a single row renderer', () => {
  renderWithI18n(
    <EntryGroupSection label="Playing">
      <ul>
        <li>Row one</li>
        <li>Row two</li>
      </ul>
    </EntryGroupSection>,
  )
  expect(screen.getByText('Row one')).toBeInTheDocument()
  expect(screen.getByText('Row two')).toBeInTheDocument()
})
