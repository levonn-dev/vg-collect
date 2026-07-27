import { screen } from '@testing-library/react'
import { MemoryRouter } from 'react-router'
import { entryFixture, sharedEntryFixture } from '../../test/fixtures'
import { renderWithMoney } from '../../test/money'
import CompactList from './CompactList'

it('renders values converted into the display currency', () => {
  renderWithMoney(
    <MemoryRouter>
      <CompactList entries={[entryFixture({ value_cents: 4200 })]} />
    </MemoryRouter>,
    { currency: 'EUR' },
  )
  expect(screen.getByText('€21.00')).toBeInTheDocument()
})

it('suppresses the name link and renders plain text when linkTo returns null', () => {
  renderWithMoney(
    <MemoryRouter>
      <CompactList entries={[entryFixture({ display_name: 'Chrono Trigger' })]} linkTo={() => null} />
    </MemoryRouter>,
  )
  expect(screen.getByText('Chrono Trigger')).toBeInTheDocument()
  expect(screen.queryByRole('link', { name: /Chrono Trigger/ })).not.toBeInTheDocument()
})

it('omits the status badge for a SharedEntry row instead of crashing, and still shows platform/value dashes', () => {
  renderWithMoney(
    <MemoryRouter>
      <CompactList
        entries={[sharedEntryFixture({ display_name: 'Someone Elses Game', platform: undefined })]}
        linkTo={() => null}
      />
    </MemoryRouter>,
  )
  expect(screen.getByText('Someone Elses Game')).toBeInTheDocument()
  expect(screen.queryByText('Backlog')).not.toBeInTheDocument()
  expect(screen.getAllByText('-').length).toBeGreaterThan(0)
})

it('omits the trailing value span when shared, leaving no dash in its place', () => {
  renderWithMoney(
    <MemoryRouter>
      <CompactList
        entries={[sharedEntryFixture({ display_name: 'Someone Elses Game' })]}
        linkTo={() => null}
        shared
      />
    </MemoryRouter>,
  )
  expect(screen.getByText('Someone Elses Game')).toBeInTheDocument()
  expect(screen.getByText('SNES')).toBeInTheDocument()
  expect(screen.queryByText('-')).not.toBeInTheDocument()
})
