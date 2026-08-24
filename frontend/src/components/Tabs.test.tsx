import { render, screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { useState } from 'react'
import { tabButtonId } from '../lib/tabs'
import Tabs, { type Tab } from './Tabs'

// Three tabs, not two: with only two, "next" and "wrap to the other
// one" look identical, which would leave wraparound unpinned.
const TABS: readonly Tab<'a' | 'b' | 'c'>[] = [
  { key: 'a', label: 'Alpha' },
  { key: 'b', label: 'Bravo' },
  { key: 'c', label: 'Charlie' },
]

function Harness() {
  const [active, setActive] = useState<'a' | 'b' | 'c'>('a')
  return <Tabs label="Demo tabs" tabs={TABS} active={active} onChange={setActive} />
}

it('exposes a labelled tablist containing every tab', () => {
  render(<Harness />)
  expect(screen.getByRole('tablist', { name: 'Demo tabs' })).toBeInTheDocument()
  expect(screen.getAllByRole('tab')).toHaveLength(3)
})

it('gives the active tab tabIndex 0 and every other tab -1 (roving tabindex)', () => {
  render(<Harness />)
  expect(screen.getByRole('tab', { name: 'Alpha' })).toHaveAttribute('tabindex', '0')
  expect(screen.getByRole('tab', { name: 'Bravo' })).toHaveAttribute('tabindex', '-1')
  expect(screen.getByRole('tab', { name: 'Charlie' })).toHaveAttribute('tabindex', '-1')
})

it('clicking an inactive tab selects it', async () => {
  const user = userEvent.setup()
  render(<Harness />)
  await user.click(screen.getByRole('tab', { name: 'Charlie' }))
  expect(screen.getByRole('tab', { name: 'Charlie' })).toHaveAttribute('aria-selected', 'true')
  expect(screen.getByRole('tab', { name: 'Alpha' })).toHaveAttribute('aria-selected', 'false')
})

it('ArrowRight moves both focus and selection to the next tab, wrapping from the last back to the first', async () => {
  const user = userEvent.setup()
  render(<Harness />)
  screen.getByRole('tab', { name: 'Alpha' }).focus()

  await user.keyboard('{ArrowRight}')
  expect(screen.getByRole('tab', { name: 'Bravo' })).toHaveAttribute('aria-selected', 'true')
  expect(screen.getByRole('tab', { name: 'Bravo' })).toHaveFocus()
  expect(screen.getByRole('tab', { name: 'Bravo' })).toHaveAttribute('tabindex', '0')
  expect(screen.getByRole('tab', { name: 'Alpha' })).toHaveAttribute('tabindex', '-1')

  await user.keyboard('{ArrowRight}')
  expect(screen.getByRole('tab', { name: 'Charlie' })).toHaveAttribute('aria-selected', 'true')

  await user.keyboard('{ArrowRight}')
  expect(screen.getByRole('tab', { name: 'Alpha' })).toHaveAttribute('aria-selected', 'true')
  expect(screen.getByRole('tab', { name: 'Alpha' })).toHaveFocus()
})

it('ArrowLeft moves both focus and selection to the previous tab, wrapping from the first back to the last', async () => {
  const user = userEvent.setup()
  render(<Harness />)
  screen.getByRole('tab', { name: 'Alpha' }).focus()

  await user.keyboard('{ArrowLeft}')
  expect(screen.getByRole('tab', { name: 'Charlie' })).toHaveAttribute('aria-selected', 'true')
  expect(screen.getByRole('tab', { name: 'Charlie' })).toHaveFocus()
})

it('a caller-supplied panel id renders as aria-controls (and the matching id) on its tab', () => {
  const tabs: readonly Tab<'a' | 'b' | 'c'>[] = [
    { key: 'a', label: 'Alpha', panelId: 'alpha-panel' },
    { key: 'b', label: 'Bravo', panelId: 'bravo-panel' },
    { key: 'c', label: 'Charlie' }, // no panel to control - stays optional
  ]
  render(<Tabs label="Demo tabs" tabs={tabs} active="a" onChange={() => {}} />)
  const alpha = screen.getByRole('tab', { name: 'Alpha' })
  expect(alpha).toHaveAttribute('aria-controls', 'alpha-panel')
  expect(alpha).toHaveAttribute('id', tabButtonId('alpha-panel'))
  expect(screen.getByRole('tab', { name: 'Bravo' })).toHaveAttribute('aria-controls', 'bravo-panel')
  const charlie = screen.getByRole('tab', { name: 'Charlie' })
  expect(charlie).not.toHaveAttribute('aria-controls')
  expect(charlie).not.toHaveAttribute('id')
})

it('a key outside ArrowLeft/ArrowRight/Home/End does nothing', async () => {
  const user = userEvent.setup()
  render(<Harness />)
  screen.getByRole('tab', { name: 'Alpha' }).focus()

  await user.keyboard('{ArrowDown}')
  expect(screen.getByRole('tab', { name: 'Alpha' })).toHaveAttribute('aria-selected', 'true')
  expect(screen.getByRole('tab', { name: 'Alpha' })).toHaveFocus()
})

it('Home and End jump focus and selection to the first and last tab', async () => {
  const user = userEvent.setup()
  render(<Harness />)
  screen.getByRole('tab', { name: 'Bravo' }).focus()

  await user.keyboard('{End}')
  expect(screen.getByRole('tab', { name: 'Charlie' })).toHaveAttribute('aria-selected', 'true')
  expect(screen.getByRole('tab', { name: 'Charlie' })).toHaveFocus()

  await user.keyboard('{Home}')
  expect(screen.getByRole('tab', { name: 'Alpha' })).toHaveAttribute('aria-selected', 'true')
  expect(screen.getByRole('tab', { name: 'Alpha' })).toHaveFocus()
})
