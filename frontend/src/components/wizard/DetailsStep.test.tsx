import { render, screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { jsonResponse } from '../../test/fixtures'
import { renderWithMoney } from '../../test/money'
import DetailsStep, { defaultDetails, detailsToCreate } from './DetailsStep'

afterEach(() => vi.unstubAllGlobals())

it('submits the collected values', async () => {
  const onNext = vi.fn()
  render(<DetailsStep heading="Copy details" currency="USD" onBack={vi.fn()} onNext={onNext} />)
  await userEvent.selectOptions(screen.getByLabelText('Packaging'), 'sealed')
  await userEvent.type(screen.getByLabelText(/price paid/i), '129.50')
  await userEvent.selectOptions(screen.getByLabelText('Status'), 'shelved')
  await userEvent.click(screen.getByRole('button', { name: 'Continue' }))
  expect(onNext).toHaveBeenCalledWith(expect.objectContaining({
    packaging: 'sealed', pricePaid: '129.50', status: 'shelved',
  }))
})

it('clears box and manual when packaging goes loose', async () => {
  const onNext = vi.fn()
  render(<DetailsStep heading="Copy details" currency="USD" onBack={vi.fn()} onNext={onNext} />)
  // The cib default carries both flags on.
  expect(screen.getByRole('checkbox', { name: /has box/i })).toBeChecked()
  expect(screen.getByRole('checkbox', { name: /has manual/i })).toBeChecked()
  await userEvent.selectOptions(screen.getByLabelText('Packaging'), 'loose')
  expect(screen.getByRole('checkbox', { name: /has box/i })).not.toBeChecked()
  expect(screen.getByRole('checkbox', { name: /has manual/i })).not.toBeChecked()
  expect(screen.queryByLabelText(/box condition/i)).not.toBeInTheDocument()
  await userEvent.click(screen.getByRole('button', { name: 'Continue' }))
  expect(onNext).toHaveBeenCalledWith(expect.objectContaining({
    packaging: 'loose', hasBox: false, hasManual: false,
  }))
})

it('checks box and manual when packaging goes cib or sealed', async () => {
  const onNext = vi.fn()
  render(<DetailsStep heading="Copy details" currency="USD" onBack={vi.fn()} onNext={onNext} />)
  await userEvent.selectOptions(screen.getByLabelText('Packaging'), 'loose')
  expect(screen.getByRole('checkbox', { name: /has box/i })).not.toBeChecked()
  await userEvent.selectOptions(screen.getByLabelText('Packaging'), 'sealed')
  expect(screen.getByRole('checkbox', { name: /has box/i })).toBeChecked()
  expect(screen.getByRole('checkbox', { name: /has manual/i })).toBeChecked()
  await userEvent.click(screen.getByRole('button', { name: 'Continue' }))
  expect(onNext).toHaveBeenCalledWith(expect.objectContaining({
    packaging: 'sealed', hasBox: true, hasManual: true,
  }))
})

it('detailsToCreate maps values onto the create contract', () => {
  const d = { ...defaultDetails(), edition: ' first print ', pricePaid: '59.99', rating: '9', hasManual: false }
  const c = detailsToCreate(d, 'USD')
  expect(c).toMatchObject({
    media_type: 'physical', region: 'ntsc_u', edition: 'first print',
    packaging: 'cib', has_box: true, has_manual: false,
    price_paid_cents: 5999, currency: 'USD', pricing_mode: 'auto',
    status: 'backlog', rating: 9, pinned: false,
  })
  expect(c.manual_condition).toBeUndefined()
  expect(c.notes).toBeUndefined()
})

// The stamp is the profile currency handed in by the caller, not
// anything read off the details - CREATE must work even while rates
// are down, which is why this never touches a rate.
it('stamps the create payload with the given currency', () => {
  const body = detailsToCreate(defaultDetails(), 'EUR')
  expect(body.currency).toBe('EUR')
})

it('does not render a currency input and labels price paid with the given currency', () => {
  render(<DetailsStep heading="Copy details" currency="EUR" onBack={vi.fn()} onNext={vi.fn()} />)
  expect(screen.queryByLabelText(/^currency$/i)).not.toBeInTheDocument()
  expect(screen.getByText(/price paid \(eur\)/i)).toBeInTheDocument()
})

it('renders no listing-match row without the callback (custom and hardware paths)', () => {
  render(<DetailsStep heading="Copy details" currency="USD" onBack={vi.fn()} onNext={vi.fn()} />)
  expect(screen.queryByText(/price listing match/i)).not.toBeInTheDocument()
  expect(screen.queryByRole('button', { name: 'Match manually' })).not.toBeInTheDocument()
})

it('opens the listing dialog, stores the pick, and clears it', async () => {
  vi.stubGlobal('fetch', vi.fn().mockImplementation((url: string) =>
    String(url).startsWith('/api/search')
      ? Promise.resolve(jsonResponse(200, {
          degraded: false,
          results: [{ type: 'pc_listing', name: 'Chrono Trigger [PAL]', pc_product_id: 7042, console_name: 'PAL Super Nintendo', loose_cents: 9800 }],
        }))
      : Promise.resolve(jsonResponse(404, {})),
  ))
  const onManualMatchChange = vi.fn()
  renderWithMoney(
    <DetailsStep heading="Copy details" currency="USD" onBack={vi.fn()} onNext={vi.fn()}
      manualMatch={null} onManualMatchChange={onManualMatchChange} manualMatchQuery="Chrono Trigger" />,
  )
  expect(screen.getByText('Price listing match (optional)')).toBeInTheDocument()
  expect(screen.getByText('Otherwise auto-match picks the listing.')).toBeInTheDocument()
  await userEvent.click(screen.getByRole('button', { name: 'Match manually' }))
  const dialog = await screen.findByRole('dialog', { name: 'Match a price listing' })
  expect(dialog).toBeInTheDocument()
  await userEvent.click(await screen.findByRole('button', { name: /use chrono trigger/i }))
  expect(onManualMatchChange).toHaveBeenCalledWith({ pcProductId: 7042, name: 'Chrono Trigger [PAL]' })
  expect(screen.queryByRole('dialog')).not.toBeInTheDocument()
})

it('renders the stored choice as a chip and clears it', async () => {
  const onManualMatchChange = vi.fn()
  render(
    <DetailsStep heading="Copy details" currency="USD" onBack={vi.fn()} onNext={vi.fn()}
      manualMatch={{ pcProductId: 7042, name: 'Chrono Trigger [PAL]' }}
      onManualMatchChange={onManualMatchChange} manualMatchQuery="Chrono Trigger" />,
  )
  expect(screen.getByText('Chrono Trigger [PAL]')).toBeInTheDocument()
  expect(screen.queryByRole('button', { name: 'Match manually' })).not.toBeInTheDocument()
  await userEvent.click(screen.getByRole('button', { name: 'Clear' }))
  expect(onManualMatchChange).toHaveBeenCalledWith(null)
})
