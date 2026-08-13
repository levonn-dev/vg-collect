import { render, screen } from '@testing-library/react'
import { MemoryRouter } from 'react-router'
import { entryFixture } from '../../test/fixtures'
import EntryLink from './EntryLink'

const entry = entryFixture({ id: 'e1' })

it('links to the default entry detail path when linkTo is unset', () => {
  render(
    <MemoryRouter>
      <EntryLink entry={entry} plainClassName="plain" linkClassName="link">Chrono Trigger</EntryLink>
    </MemoryRouter>,
  )
  expect(screen.getByRole('link', { name: 'Chrono Trigger' })).toHaveAttribute('href', '/entries/e1')
  expect(screen.getByRole('link')).toHaveClass('link', { exact: true })
})

it('links to the caller-supplied target when linkTo returns a string', () => {
  render(
    <MemoryRouter>
      <EntryLink entry={entry} linkTo={() => '/shared/xyz'} plainClassName="plain" linkClassName="link">
        Chrono Trigger
      </EntryLink>
    </MemoryRouter>,
  )
  expect(screen.getByRole('link', { name: 'Chrono Trigger' })).toHaveAttribute('href', '/shared/xyz')
})

it('renders plain text (no link) when linkTo returns null, as a span by default', () => {
  render(
    <MemoryRouter>
      <EntryLink entry={entry} linkTo={() => null} plainClassName="plain" linkClassName="link">
        Chrono Trigger
      </EntryLink>
    </MemoryRouter>,
  )
  expect(screen.queryByRole('link')).not.toBeInTheDocument()
  const plain = screen.getByText('Chrono Trigger')
  expect(plain.tagName).toBe('SPAN')
  expect(plain).toHaveClass('plain', { exact: true })
})

it('renders the plain branch as the caller-supplied tag (CoverGrid uses div)', () => {
  render(
    <MemoryRouter>
      <EntryLink entry={entry} linkTo={() => null} as="div" plainClassName="block" linkClassName="block">
        cover content
      </EntryLink>
    </MemoryRouter>,
  )
  expect(screen.getByText('cover content').tagName).toBe('DIV')
})
