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
