import { render, screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import DetailsStep, { defaultDetails, detailsToCreate } from './DetailsStep'

it('submits the collected values', async () => {
  const onNext = vi.fn()
  render(<DetailsStep heading="Copy details" onBack={vi.fn()} onNext={onNext} />)
  await userEvent.selectOptions(screen.getByLabelText('Packaging'), 'sealed')
  await userEvent.type(screen.getByLabelText('Price paid'), '129.50')
  await userEvent.selectOptions(screen.getByLabelText('Status'), 'shelved')
  await userEvent.click(screen.getByRole('button', { name: 'Continue' }))
  expect(onNext).toHaveBeenCalledWith(expect.objectContaining({
    packaging: 'sealed', pricePaid: '129.50', status: 'shelved',
  }))
})

it('clears box and manual when packaging goes loose', async () => {
  const onNext = vi.fn()
  render(<DetailsStep heading="Copy details" onBack={vi.fn()} onNext={onNext} />)
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
  render(<DetailsStep heading="Copy details" onBack={vi.fn()} onNext={onNext} />)
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
  const c = detailsToCreate(d)
  expect(c).toMatchObject({
    media_type: 'physical', region: 'ntsc_u', edition: 'first print',
    packaging: 'cib', has_box: true, has_manual: false,
    price_paid_cents: 5999, currency: 'USD', pricing_mode: 'auto',
    status: 'backlog', rating: 9, pinned: false,
  })
  expect(c.manual_condition).toBeUndefined()
  expect(c.notes).toBeUndefined()
})
