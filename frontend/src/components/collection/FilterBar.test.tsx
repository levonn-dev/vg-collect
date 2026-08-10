import { screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { defaultListState } from '../../lib/listParams'
import { renderWithI18n } from '../../test/i18n'
import FilterBar from './FilterBar'

const platforms = [{ id: 6, name: 'SNES' }, { id: 7, name: 'PlayStation' }]
const tags = [{ id: 't1', name: 'rpg', entry_count: 2 }]
const developers = ['Retro Studios', 'Square']
const publishers = ['Nintendo']

it('toggles a status filter through onChange', async () => {
  const onChange = vi.fn()
  renderWithI18n(<FilterBar state={defaultListState()} platforms={platforms} tags={tags} onChange={onChange} />)
  await userEvent.click(screen.getByRole('checkbox', { name: 'Backlog' }))
  expect(onChange).toHaveBeenCalledWith(expect.objectContaining({ status: ['backlog'] }))
})

it('toggles developer and publisher filters through onChange', async () => {
  const onChange = vi.fn()
  renderWithI18n(
    <FilterBar state={defaultListState()} platforms={platforms} tags={tags}
      developers={developers} publishers={publishers} onChange={onChange} />,
  )
  await userEvent.click(screen.getByRole('checkbox', { name: 'Retro Studios' }))
  expect(onChange).toHaveBeenCalledWith(expect.objectContaining({ developer: ['Retro Studios'] }))
  await userEvent.click(screen.getByRole('checkbox', { name: 'Nintendo' }))
  expect(onChange).toHaveBeenCalledWith(expect.objectContaining({ publisher: ['Nintendo'] }))
})

it('unchecks an active filter', async () => {
  const onChange = vi.fn()
  const state = { ...defaultListState(), status: ['backlog' as const], platformId: [6] }
  renderWithI18n(<FilterBar state={state} platforms={platforms} tags={tags} onChange={onChange} />)
  expect(screen.getByRole('checkbox', { name: 'Backlog' })).toBeChecked()
  await userEvent.click(screen.getByRole('checkbox', { name: 'SNES' }))
  expect(onChange).toHaveBeenCalledWith(expect.objectContaining({ platformId: [] }))
})

it('renders every chip fieldset plus the platform and tag checkboxes', () => {
  renderWithI18n(<FilterBar state={defaultListState()} platforms={platforms} tags={tags} onChange={vi.fn()} />)
  expect(screen.getByRole('group', { name: 'Status' })).toBeInTheDocument()
  expect(screen.getByRole('group', { name: 'Type' })).toBeInTheDocument()
  expect(screen.getByRole('group', { name: 'Packaging' })).toBeInTheDocument()
  expect(screen.getByRole('group', { name: 'Region' })).toBeInTheDocument()
  expect(screen.getByRole('group', { name: 'Condition' })).toBeInTheDocument()
  expect(screen.getByRole('checkbox', { name: 'SNES' })).toBeInTheDocument()
  expect(screen.getByRole('checkbox', { name: 'rpg' })).toBeInTheDocument()
})

it('toggles a tag filter through onChange', async () => {
  const onChange = vi.fn()
  renderWithI18n(<FilterBar state={defaultListState()} platforms={platforms} tags={tags} onChange={onChange} />)
  await userEvent.click(screen.getByRole('checkbox', { name: 'rpg' }))
  expect(onChange).toHaveBeenCalledWith(expect.objectContaining({ tagId: ['t1'] }))
})
