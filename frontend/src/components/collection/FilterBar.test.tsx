import { render, screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import type { ListState } from '../../lib/listParams'
import { defaultListState } from '../../lib/listParams'
import FilterBar from './FilterBar'

const platforms = [{ id: 6, name: 'SNES' }, { id: 7, name: 'PlayStation' }]
const tags = [{ id: 't1', name: 'rpg', entry_count: 2 }]

it('toggles a status filter through onChange', async () => {
  const onChange = vi.fn()
  render(<FilterBar state={defaultListState()} platforms={platforms} tags={tags} onChange={onChange} />)
  await userEvent.click(screen.getByRole('checkbox', { name: 'Backlog' }))
  expect(onChange).toHaveBeenCalledWith(expect.objectContaining({ status: ['backlog'] }))
})

it('unchecks an active filter', async () => {
  const onChange = vi.fn()
  const state = { ...defaultListState(), status: ['backlog' as const], platformId: [6] }
  render(<FilterBar state={state} platforms={platforms} tags={tags} onChange={onChange} />)
  expect(screen.getByRole('checkbox', { name: 'Backlog' })).toBeChecked()
  await userEvent.click(screen.getByRole('checkbox', { name: 'SNES' }))
  expect(onChange).toHaveBeenCalledWith(expect.objectContaining({ platformId: [] }))
})

it('offers the backlog-order sort only over a pure backlog filter', () => {
  const { unmount } = render(
    <FilterBar state={defaultListState()} platforms={platforms} tags={tags} onChange={vi.fn()} />,
  )
  expect(screen.queryByRole('option', { name: 'Backlog order' })).not.toBeInTheDocument()
  unmount()
  render(
    <FilterBar
      state={{ ...defaultListState(), status: ['backlog'] }}
      platforms={platforms}
      tags={tags}
      onChange={vi.fn()}
    />,
  )
  expect(screen.getByRole('option', { name: 'Backlog order' })).toBeInTheDocument()
})

it('changes sort and flips order', async () => {
  const onChange = vi.fn()
  render(<FilterBar state={{ ...defaultListState(), sort: 'value' }} platforms={platforms} tags={tags} onChange={onChange} />)
  await userEvent.selectOptions(screen.getByLabelText('Sort'), 'rating')
  expect(onChange).toHaveBeenCalledWith(expect.objectContaining({ sort: 'rating' }))
  await userEvent.click(screen.getByRole('button', { name: /order/i }))
  expect(onChange).toHaveBeenCalledWith(expect.objectContaining({ order: 'asc' }))
})

it('clears everything', async () => {
  const onChange = vi.fn()
  const state = { ...defaultListState(), status: ['beaten' as const], tagId: ['t1'], sort: 'value' as const }
  render(<FilterBar state={state} platforms={platforms} tags={tags} onChange={onChange} />)
  await userEvent.click(screen.getByRole('button', { name: /clear filters/i }))
  const next = onChange.mock.calls[0][0] as ListState
  expect(next.status).toEqual([])
  expect(next.tagId).toEqual([])
  expect(next.sort).toBeUndefined()
})
