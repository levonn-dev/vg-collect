import { screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import type { ListState } from '../../lib/listParams'
import { defaultListState } from '../../lib/listParams'
import { renderWithI18n } from '../../test/i18n'
import ListControls from './ListControls'

function renderControls(
  state: ListState = defaultListState(),
  filtersOpen = false,
  bulkMode = false,
  bulkAvailable = true,
) {
  const onApply = vi.fn()
  const onChange = vi.fn()
  const onToggleFilters = vi.fn()
  const onToggleBulk = vi.fn()
  const { unmount } = renderWithI18n(
    <ListControls
      state={state}
      onApply={onApply}
      onChange={onChange}
      filtersOpen={filtersOpen}
      onToggleFilters={onToggleFilters}
      bulkMode={bulkMode}
      onToggleBulk={onToggleBulk}
      bulkAvailable={bulkAvailable}
    />,
  )
  return { onApply, onChange, onToggleFilters, onToggleBulk, unmount }
}

it('renders the display mode group, sort, order, group by, and Filters controls', () => {
  renderControls()
  expect(screen.getByRole('group', { name: 'Display mode' })).toBeInTheDocument()
  expect(screen.getByRole('button', { name: 'Table' })).toBeInTheDocument()
  expect(screen.getByRole('button', { name: 'Covers' })).toBeInTheDocument()
  expect(screen.getByRole('button', { name: 'Compact' })).toBeInTheDocument()
  expect(screen.getByLabelText('Sort')).toBeInTheDocument()
  expect(screen.getByRole('button', { name: /^Order:/ })).toBeInTheDocument()
  expect(screen.getByLabelText('Group by')).toBeInTheDocument()
  expect(screen.getByRole('button', { name: 'Filters' })).toBeInTheDocument()
})

it('shows no count when no filter dimension is active', () => {
  renderControls()
  expect(screen.getByRole('button', { name: 'Filters' })).toBeInTheDocument()
})

it('counts active filter dimensions, not selected values, on the Filters badge', () => {
  // Three dimensions active (status, platformId, developer) even though
  // status alone carries two selected values - the badge counts
  // dimensions, and the credit dimensions count like any other.
  const state = {
    ...defaultListState(),
    status: ['backlog' as const, 'playing' as const], platformId: [6], developer: ['Square'],
  }
  renderControls(state)
  expect(screen.getByRole('button', { name: 'Filters (3)' })).toBeInTheDocument()
})

it('reflects the closed panel state via aria-expanded and calls back on click', async () => {
  const { onToggleFilters } = renderControls()
  const toggle = screen.getByRole('button', { name: 'Filters' })
  expect(toggle).toHaveAttribute('aria-expanded', 'false')
  await userEvent.click(toggle)
  expect(onToggleFilters).toHaveBeenCalledTimes(1)
})

it('reflects an open panel via aria-expanded', () => {
  renderControls(defaultListState(), true)
  expect(screen.getByRole('button', { name: 'Filters' })).toHaveAttribute('aria-expanded', 'true')
})

it('hides Clear filters at defaults', () => {
  renderControls()
  expect(screen.queryByRole('button', { name: /clear filters/i })).not.toBeInTheDocument()
})

it('shows Clear filters once a filter dimension is active', () => {
  renderControls({ ...defaultListState(), status: ['backlog' as const] })
  expect(screen.getByRole('button', { name: /clear filters/i })).toBeInTheDocument()
})

it('shows Clear filters once sort is set even with no dimensions selected', () => {
  renderControls({ ...defaultListState(), sort: 'value' as const })
  expect(screen.getByRole('button', { name: /clear filters/i })).toBeInTheDocument()
})

it('shows Clear filters once order is set even with no dimensions or sort', () => {
  renderControls({ ...defaultListState(), order: 'asc' as const })
  expect(screen.getByRole('button', { name: /clear filters/i })).toBeInTheDocument()
})

it('shows Clear filters once group by is set even with no dimensions or sort', () => {
  renderControls({ ...defaultListState(), groupBy: 'platform' as const })
  expect(screen.getByRole('button', { name: /clear filters/i })).toBeInTheDocument()
})

it('clears everything back to defaults except mode', async () => {
  const state = {
    ...defaultListState(),
    status: ['beaten' as const],
    tagId: ['t1'],
    sort: 'value' as const,
    mode: 'grid' as const,
  }
  const { onChange } = renderControls(state)
  await userEvent.click(screen.getByRole('button', { name: /clear filters/i }))
  const next = onChange.mock.calls[0][0] as ListState
  expect(next.status).toEqual([])
  expect(next.tagId).toEqual([])
  expect(next.sort).toBeUndefined()
  expect(next.mode).toBe('grid')
})

it('offers the backlog-order sort only over a pure backlog filter', () => {
  const { unmount } = renderControls()
  expect(screen.queryByRole('option', { name: 'Backlog order' })).not.toBeInTheDocument()
  unmount()
  renderControls({ ...defaultListState(), status: ['backlog' as const] })
  expect(screen.getByRole('option', { name: 'Backlog order' })).toBeInTheDocument()
})

it('changes sort and flips order through onChange', async () => {
  const { onChange } = renderControls({ ...defaultListState(), sort: 'value' as const })
  await userEvent.selectOptions(screen.getByLabelText('Sort'), 'rating')
  expect(onChange).toHaveBeenCalledWith(expect.objectContaining({ sort: 'rating' }))
  await userEvent.click(screen.getByRole('button', { name: /^Order:/ }))
  expect(onChange).toHaveBeenCalledWith(expect.objectContaining({ order: 'asc' }))
})

it('changes group by through onChange', async () => {
  const { onChange } = renderControls()
  await userEvent.selectOptions(screen.getByLabelText('Group by'), 'platform')
  expect(onChange).toHaveBeenCalledWith(expect.objectContaining({ groupBy: 'platform' }))
})

it('switches display mode through onApply, not onChange, preserving the current page', async () => {
  const { onApply, onChange } = renderControls({ ...defaultListState(), page: 3 })
  await userEvent.click(screen.getByRole('button', { name: 'Covers' }))
  expect(onApply).toHaveBeenCalledWith(expect.objectContaining({ mode: 'grid', page: 3 }))
  expect(onChange).not.toHaveBeenCalled()
})

it('shows Bulk edit only in table display mode', () => {
  const table = renderControls({ ...defaultListState(), mode: 'table' })
  expect(screen.getByRole('button', { name: 'Bulk edit' })).toBeInTheDocument()
  table.unmount()

  const grid = renderControls({ ...defaultListState(), mode: 'grid' })
  expect(screen.queryByRole('button', { name: 'Bulk edit' })).not.toBeInTheDocument()
  grid.unmount()

  renderControls({ ...defaultListState(), mode: 'compact' })
  expect(screen.queryByRole('button', { name: 'Bulk edit' })).not.toBeInTheDocument()
})

it('renders the Bulk edit toggle only when bulkAvailable is true, even in table mode', () => {
  const unavailable = renderControls({ ...defaultListState(), mode: 'table' }, false, false, false)
  expect(screen.queryByRole('button', { name: 'Bulk edit' })).not.toBeInTheDocument()
  unavailable.unmount()

  renderControls({ ...defaultListState(), mode: 'table' }, false, false, true)
  expect(screen.getByRole('button', { name: 'Bulk edit' })).toBeInTheDocument()
})

it('reflects bulkMode through aria-pressed in both states and calls back on click', async () => {
  const { onToggleBulk, unmount } = renderControls(defaultListState(), false, false)
  expect(screen.getByRole('button', { name: 'Bulk edit' })).toHaveAttribute('aria-pressed', 'false')
  await userEvent.click(screen.getByRole('button', { name: 'Bulk edit' }))
  expect(onToggleBulk).toHaveBeenCalledTimes(1)
  unmount()
  renderControls(defaultListState(), false, true)
  expect(screen.getByRole('button', { name: 'Bulk edit' })).toHaveAttribute('aria-pressed', 'true')
})
