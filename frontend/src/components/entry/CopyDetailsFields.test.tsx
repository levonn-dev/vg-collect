import { screen, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { useState } from 'react'
import { renderWithI18n } from '../../test/i18n'
import type { CopyDetailsValues } from './CopyDetailsFields'
import { CopyDetailsFields } from './CopyDetailsFields'

function baseValues(): CopyDetailsValues {
  return {
    region: 'ntsc_u', edition: '', packaging: 'cib', hasBox: true, hasManual: true,
    boxCondition: '', manualCondition: '', itemCondition: '', pricePaid: '',
    purchasedAt: '', purchasedFrom: '', status: 'backlog',
    rating: '', notes: '', storageLocation: '', pinned: false,
  }
}

// The component is controlled, so the harness owns the value the way a
// host form does; the spy sees every onChange before it lands.
function Harness({ initial, onChange }: { initial?: Partial<CopyDetailsValues>; onChange?: (next: CopyDetailsValues) => void }) {
  const [v, setV] = useState({ ...baseValues(), ...initial })
  return (
    <CopyDetailsFields
      value={v}
      onChange={(next) => {
        onChange?.(next)
        setV(next)
      }}
      currencyLabel="USD"
      editionLabel="Edition"
      editionPlaceholder="first print..."
    />
  )
}

it('going loose clears both flags and hides the gated condition selects, keeping stored grades', async () => {
  const onChange = vi.fn()
  renderWithI18n(<Harness initial={{ boxCondition: 'good', manualCondition: 'good' }} onChange={onChange} />)
  expect(screen.getByRole('checkbox', { name: /has box/i })).toBeChecked()
  expect(screen.getByRole('checkbox', { name: /has manual/i })).toBeChecked()
  await userEvent.selectOptions(screen.getByLabelText('Packaging'), 'loose')
  expect(screen.getByRole('checkbox', { name: /has box/i })).not.toBeChecked()
  expect(screen.getByRole('checkbox', { name: /has manual/i })).not.toBeChecked()
  expect(screen.queryByLabelText(/box condition/i)).not.toBeInTheDocument()
  expect(screen.queryByLabelText(/manual condition/i)).not.toBeInTheDocument()
  // The rule flips the flags only; stored grades ride along untouched
  // (the wire mappers drop them while the flags are off).
  expect(onChange).toHaveBeenCalledWith({
    ...baseValues(), boxCondition: 'good', manualCondition: 'good',
    packaging: 'loose', hasBox: false, hasManual: false,
  })
})

it('going cib or sealed checks both flags again', async () => {
  renderWithI18n(<Harness />)
  await userEvent.selectOptions(screen.getByLabelText('Packaging'), 'loose')
  expect(screen.getByRole('checkbox', { name: /has box/i })).not.toBeChecked()
  await userEvent.selectOptions(screen.getByLabelText('Packaging'), 'sealed')
  expect(screen.getByRole('checkbox', { name: /has box/i })).toBeChecked()
  expect(screen.getByRole('checkbox', { name: /has manual/i })).toBeChecked()
  expect(screen.getByLabelText(/box condition/i)).toBeInTheDocument()
})

it('condition selects lead with the Not graded empty option', () => {
  renderWithI18n(<Harness />)
  for (const label of [/item condition/i, /box condition/i, /manual condition/i]) {
    const first = within(screen.getByLabelText(label)).getAllByRole('option')[0]
    expect(first).toHaveTextContent('Not graded')
    expect(first).toHaveAttribute('value', '')
  }
})

it('rating offers Unrated plus 1 through 10', () => {
  renderWithI18n(<Harness />)
  const options = within(screen.getByLabelText('Rating')).getAllByRole('option')
  expect(options.map((o) => o.getAttribute('value')))
    .toEqual(['', '1', '2', '3', '4', '5', '6', '7', '8', '9', '10'])
  expect(options[0]).toHaveTextContent('Unrated')
})

it('onChange hands back the full next value on a single field edit', async () => {
  const onChange = vi.fn()
  renderWithI18n(<Harness onChange={onChange} />)
  await userEvent.selectOptions(screen.getByLabelText(/item condition/i), 'near_mint')
  expect(onChange).toHaveBeenCalledTimes(1)
  expect(onChange).toHaveBeenCalledWith({ ...baseValues(), itemCondition: 'near_mint' })
  onChange.mockClear()
  await userEvent.type(screen.getByLabelText(/storage location/i), 'A')
  expect(onChange).toHaveBeenCalledWith({ ...baseValues(), itemCondition: 'near_mint', storageLocation: 'A' })
})

it('selects render translated labels off the shared maps, not raw wire values', () => {
  renderWithI18n(<Harness />)
  const condition = screen.getByLabelText(/item condition/i)
  expect(within(condition).getByRole('option', { name: 'Near mint' })).toBeInTheDocument()
  expect(within(condition).queryByRole('option', { name: 'near_mint' })).not.toBeInTheDocument()
  // The status and packaging wire labels read like their values on
  // purpose; what matters is that the known values all stay offered.
  expect(within(screen.getByLabelText('Status')).getByRole('option', { name: 'backlog' })).toBeInTheDocument()
  expect(within(screen.getByLabelText('Packaging')).getByRole('option', { name: 'sealed' })).toBeInTheDocument()
})

it('labels price paid with the given currency and edition with the given wording', () => {
  renderWithI18n(<Harness />)
  expect(screen.getByText(/price paid \(usd\)/i)).toBeInTheDocument()
  expect(screen.getByLabelText('Edition')).toHaveAttribute('placeholder', 'first print...')
})
