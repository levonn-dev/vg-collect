import { i18n } from '@lingui/core'
import { act, cleanup, screen, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { useState } from 'react'
import { MemoryRouter } from 'react-router'
import type { Entry } from '../../api/collection'
import { messages as jaMessages } from '../../locales/ja.po'
import { entryFixture, sharedEntryFixture } from '../../test/fixtures'
import { renderWithMoney } from '../../test/money'
import EntryTable from './EntryTable'

const renderTable = (entries: Entry[], opts: { currency?: string } = {}) =>
  renderWithMoney(
    <MemoryRouter>
      <EntryTable entries={entries} />
    </MemoryRouter>,
    opts,
  )

afterEach(() => {
  // Runs ahead of RTL's cleanup, else I18nProvider updates outside act;
  // leaves en active for the shared module-level singleton.
  cleanup()
  i18n.activate('en')
})

function activateJa() {
  i18n.load('ja', jaMessages)
  i18n.activate('ja')
}

// Region-localized entry with a native-script title, transliteration, and its
// own box art.
const jp: Partial<Entry> = {
  display_name: 'Trials of Mana',
  localized_name: '聖剣伝説 3',
  localized_name_translit: 'Seiken Densetsu 3',
  localized_cover_url: 'https://x/jp.jpg',
  region: 'ntsc_j',
}

it('renders values converted into the display currency, header labeled', () => {
  renderTable([entryFixture({ value_cents: 4200 })], { currency: 'EUR' })
  expect(screen.getByText('Value (EUR)')).toBeInTheDocument()
  expect(screen.getByText('€21.00')).toBeInTheDocument()
})

it('pins a matching entered pair instead of converting the snapshot', () => {
  renderTable(
    [
      entryFixture({
        pricing_mode: 'custom',
        value_cents: 11900,
        custom_value_cents: 11900,
        custom_value_entered_cents: 6000,
        custom_value_entered_currency: 'EUR',
      }),
    ],
    { currency: 'EUR' },
  )
  expect(screen.getByText('€60.00')).toBeInTheDocument()
})

it('leaves the paid column in its stored currency', () => {
  renderTable([entryFixture({ price_paid_cents: 5000, currency: 'JPY' })], { currency: 'EUR' })
  expect(screen.getByText('¥50')).toBeInTheDocument()
})

it('suppresses the name link and renders plain text when linkTo returns null', () => {
  renderWithMoney(
    <MemoryRouter>
      <EntryTable entries={[entryFixture({ display_name: 'Chrono Trigger' })]} linkTo={() => null} />
    </MemoryRouter>,
  )
  expect(screen.getByText('Chrono Trigger')).toBeInTheDocument()
  expect(screen.queryByRole('link', { name: /Chrono Trigger/ })).not.toBeInTheDocument()
})

it('still links to /entries/:id by default when linkTo is not passed', () => {
  const e = entryFixture({ display_name: 'Chrono Trigger' })
  renderWithMoney(
    <MemoryRouter>
      <EntryTable entries={[e]} />
    </MemoryRouter>,
  )
  expect(screen.getByRole('link', { name: 'Chrono Trigger' })).toHaveAttribute('href', `/entries/${e.id}`)
})

it('numbers rows 1-based over this render, only when numbered is set', () => {
  renderWithMoney(
    <MemoryRouter>
      <EntryTable
        entries={[entryFixture({ display_name: 'First' }), entryFixture({ display_name: 'Second' })]}
        numbered
      />
    </MemoryRouter>,
  )
  const rows = screen.getAllByRole('row').slice(1) // drop the header row
  expect(within(rows[0]).getByRole('cell', { name: '1' })).toBeInTheDocument()
  expect(within(rows[1]).getByRole('cell', { name: '2' })).toBeInTheDocument()
})

it('omits the rank column by default', () => {
  renderWithMoney(
    <MemoryRouter>
      <EntryTable entries={[entryFixture()]} />
    </MemoryRouter>,
  )
  expect(screen.queryByRole('columnheader', { name: '#' })).not.toBeInTheDocument()
})

it('falls back to a dash in the status, rating, and paid columns for a SharedEntry row, without crashing', () => {
  renderWithMoney(
    <MemoryRouter>
      <EntryTable entries={[sharedEntryFixture({ display_name: 'Someone Elses Game' })]} linkTo={() => null} />
    </MemoryRouter>,
  )
  const row = screen.getAllByRole('row')[1]
  const cells = within(row).getAllByRole('cell')
  // indices 2/4/5 (Status/Rating/Paid) are columns SharedEntry can't fill;
  // index 3 (Packaging) is in the whitelist and still renders.
  expect(cells[2]).toHaveTextContent('-')
  expect(cells[3]).toHaveTextContent('cib')
  expect(cells[4]).toHaveTextContent('-')
  expect(cells[5]).toHaveTextContent('-')
})

it('omits the Status, Rating, Paid, and Value columns entirely when shared, keeping Name/Platform/Packaging', () => {
  renderWithMoney(
    <MemoryRouter>
      <EntryTable
        entries={[sharedEntryFixture({ display_name: 'Someone Elses Game' })]}
        linkTo={() => null}
        shared
      />
    </MemoryRouter>,
  )
  expect(screen.queryByRole('columnheader', { name: 'Status' })).not.toBeInTheDocument()
  expect(screen.queryByRole('columnheader', { name: 'Rating' })).not.toBeInTheDocument()
  expect(screen.queryByRole('columnheader', { name: 'Paid' })).not.toBeInTheDocument()
  expect(screen.queryByRole('columnheader', { name: /^Value/ })).not.toBeInTheDocument()
  expect(screen.getByRole('columnheader', { name: 'Name' })).toBeInTheDocument()
  expect(screen.getByRole('columnheader', { name: 'Platform' })).toBeInTheDocument()
  expect(screen.getByRole('columnheader', { name: 'Packaging' })).toBeInTheDocument()

  // No dead '-' cells: the row is exactly Name, Platform, Packaging.
  const row = screen.getAllByRole('row')[1]
  const cells = within(row).getAllByRole('cell')
  expect(cells).toHaveLength(3)
  expect(cells[2]).toHaveTextContent('cib')
})

it('renders no checkboxes without selectable', () => {
  renderTable([entryFixture({ display_name: 'Chrono Trigger' })])
  expect(screen.queryByRole('checkbox')).not.toBeInTheDocument()
})

it('a row checkbox is named for the entry, reflects the selected set, and toggles through onToggleSelect', async () => {
  const e = entryFixture({ display_name: 'Chrono Trigger' })
  const onToggleSelect = vi.fn()
  renderWithMoney(
    <MemoryRouter>
      <EntryTable entries={[e]} selectable selected={new Set()} onToggleSelect={onToggleSelect} />
    </MemoryRouter>,
  )
  const box = screen.getByRole('checkbox', { name: 'Select Chrono Trigger' })
  expect(box).not.toBeChecked()
  await userEvent.click(box)
  expect(onToggleSelect).toHaveBeenCalledWith(e.id)
})

it('reflects an already-selected row as checked', () => {
  const e = entryFixture({ display_name: 'Chrono Trigger' })
  renderWithMoney(
    <MemoryRouter>
      <EntryTable entries={[e]} selectable selected={new Set([e.id])} onToggleSelect={vi.fn()} />
    </MemoryRouter>,
  )
  expect(screen.getByRole('checkbox', { name: 'Select Chrono Trigger' })).toBeChecked()
})

it('the header checkbox is unchecked, indeterminate, or checked to match none/some/all selected', () => {
  const a = entryFixture({ display_name: 'A' })
  const b = entryFixture({ display_name: 'B' })
  const selectAll = () => screen.getByRole<HTMLInputElement>('checkbox', { name: 'Select all' })

  // Three separate mounts, not rerender: renderWithMoney's QueryClientProvider
  // wraps only the tree passed to it, so rerender would drop the provider.
  const none = renderWithMoney(
    <MemoryRouter>
      <EntryTable entries={[a, b]} selectable selected={new Set()} onToggleSelect={vi.fn()} />
    </MemoryRouter>,
  )
  expect(selectAll()).not.toBeChecked()
  expect(selectAll().indeterminate).toBe(false)
  none.unmount()

  const some = renderWithMoney(
    <MemoryRouter>
      <EntryTable entries={[a, b]} selectable selected={new Set([a.id])} onToggleSelect={vi.fn()} />
    </MemoryRouter>,
  )
  expect(selectAll()).not.toBeChecked()
  expect(selectAll().indeterminate).toBe(true)
  some.unmount()

  renderWithMoney(
    <MemoryRouter>
      <EntryTable entries={[a, b]} selectable selected={new Set([a.id, b.id])} onToggleSelect={vi.fn()} />
    </MemoryRouter>,
  )
  expect(selectAll()).toBeChecked()
  expect(selectAll().indeterminate).toBe(false)
})

it('clicking select-all with some or none selected selects only the not-yet-selected rows', async () => {
  const a = entryFixture({ display_name: 'A' })
  const b = entryFixture({ display_name: 'B' })
  const onToggleSelect = vi.fn()
  renderWithMoney(
    <MemoryRouter>
      <EntryTable entries={[a, b]} selectable selected={new Set([a.id])} onToggleSelect={onToggleSelect} />
    </MemoryRouter>,
  )
  await userEvent.click(screen.getByRole('checkbox', { name: 'Select all' }))
  expect(onToggleSelect).toHaveBeenCalledTimes(1)
  expect(onToggleSelect).toHaveBeenCalledWith(b.id)
})

it('clicking select-all with every row selected clears them all', async () => {
  const a = entryFixture({ display_name: 'A' })
  const b = entryFixture({ display_name: 'B' })
  const onToggleSelect = vi.fn()
  renderWithMoney(
    <MemoryRouter>
      <EntryTable entries={[a, b]} selectable selected={new Set([a.id, b.id])} onToggleSelect={onToggleSelect} />
    </MemoryRouter>,
  )
  await userEvent.click(screen.getByRole('checkbox', { name: 'Select all' }))
  expect(onToggleSelect).toHaveBeenCalledTimes(2)
  expect(onToggleSelect).toHaveBeenCalledWith(a.id)
  expect(onToggleSelect).toHaveBeenCalledWith(b.id)
})

function SharedSelectionHarness({ groupA, groupB }: { groupA: Entry[]; groupB: Entry[] }) {
  const [selected, setSelected] = useState<Set<string>>(new Set())
  const toggle = (id: string) =>
    setSelected((prev) => {
      const next = new Set(prev)
      if (next.has(id)) next.delete(id)
      else next.add(id)
      return next
    })
  return (
    <>
      <section aria-label="Group A">
        <EntryTable entries={groupA} selectable selected={selected} onToggleSelect={toggle} />
      </section>
      <section aria-label="Group B">
        <EntryTable entries={groupB} selectable selected={selected} onToggleSelect={toggle} />
      </section>
    </>
  )
}

it('shares one selection Set across independently-managed per-table select-all checkboxes', async () => {
  const a1 = entryFixture({ display_name: 'A1' })
  const b1 = entryFixture({ display_name: 'B1' })
  renderWithMoney(
    <MemoryRouter>
      <SharedSelectionHarness groupA={[a1]} groupB={[b1]} />
    </MemoryRouter>,
  )
  const groupA = within(screen.getByRole('region', { name: 'Group A' }))
  const groupB = within(screen.getByRole('region', { name: 'Group B' }))

  await userEvent.click(groupA.getByRole('checkbox', { name: 'Select all' }))
  expect(groupA.getByRole('checkbox', { name: 'Select A1' })).toBeChecked()
  expect(groupB.getByRole('checkbox', { name: 'Select all' })).not.toBeChecked()
  expect(groupB.getByRole('checkbox', { name: 'Select B1' })).not.toBeChecked()

  await userEvent.click(groupB.getByRole('checkbox', { name: 'Select all' }))
  expect(groupA.getByRole('checkbox', { name: 'Select A1' })).toBeChecked() // untouched by B's select-all
  expect(groupB.getByRole('checkbox', { name: 'Select B1' })).toBeChecked()
})

it('renders the romanized title with a ja-Latn lang attribute by default', () => {
  renderTable([entryFixture(jp)])
  expect(screen.getByText('Seiken Densetsu 3')).toHaveAttribute('lang', 'ja-Latn')
})

it('renders the native title with a ja lang attribute under the ja locale', () => {
  activateJa()
  renderTable([entryFixture(jp)])
  expect(screen.getByText('聖剣伝説 3')).toHaveAttribute('lang', 'ja')
})

it('leaves the lang attribute off a canonical-only title', () => {
  renderTable([entryFixture({ display_name: 'Chrono Trigger' })])
  expect(screen.getByText('Chrono Trigger')).not.toHaveAttribute('lang')
})

it('tracks a live locale switch on the same mounted row without remounting', () => {
  renderTable([entryFixture(jp)])
  expect(screen.getByText('Seiken Densetsu 3')).toBeInTheDocument()
  act(() => activateJa())
  expect(screen.getByText('聖剣伝説 3')).toBeInTheDocument()
  expect(screen.queryByText('Seiken Densetsu 3')).not.toBeInTheDocument()
})

it('names a row checkbox for the localized title shown in the row, not the raw display_name', () => {
  renderWithMoney(
    <MemoryRouter>
      <EntryTable entries={[entryFixture(jp)]} selectable selected={new Set()} onToggleSelect={vi.fn()} />
    </MemoryRouter>,
  )
  expect(screen.getByRole('checkbox', { name: 'Select Seiken Densetsu 3' })).toBeInTheDocument()
  expect(screen.queryByRole('checkbox', { name: 'Select Trials of Mana' })).not.toBeInTheDocument()
})
