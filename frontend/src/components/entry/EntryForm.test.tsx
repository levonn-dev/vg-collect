import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { fireEvent, render, screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import type { EntryUpdate } from '../../api/collection'
import { entryFixture, jsonResponse } from '../../test/fixtures'
import EntryForm from './EntryForm'

function renderForm(entry = entryFixture(), onSave = vi.fn()) {
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } })
  vi.stubGlobal('fetch', vi.fn().mockResolvedValue(jsonResponse(200, { tags: [] })))
  const view = render(
    <QueryClientProvider client={qc}>
      <EntryForm entry={entry} onSave={onSave} saving={false} saved={false} error={null} />
    </QueryClientProvider>,
  )
  return { onSave, unmount: view.unmount }
}

afterEach(() => vi.unstubAllGlobals())

it('gates box and manual condition on their checkboxes', async () => {
  renderForm(entryFixture({ has_box: false, has_manual: false, box_condition: undefined, manual_condition: undefined }))
  expect(screen.queryByLabelText(/box condition/i)).not.toBeInTheDocument()
  await userEvent.click(screen.getByRole('checkbox', { name: /has box/i }))
  expect(screen.getByLabelText(/box condition/i)).toBeInTheDocument()
})

it('hides display fields on product-backed entries and shows them on customs', async () => {
  const { unmount } = renderForm(entryFixture())
  expect(screen.queryByLabelText(/^name$/i)).not.toBeInTheDocument()
  unmount()
  renderForm(entryFixture({ product_id: undefined, display_name: 'Repro Cart' }))
  expect(await screen.findByLabelText(/^name$/i)).toHaveValue('Repro Cart')
})

it('submits a faithful full-replacement payload with the edits applied', async () => {
  const entry = entryFixture({
    product_id: 'p1', notes: 'old note', rating: 7, price_paid_cents: 1500,
    pricing_mode: 'proxy', pricing_product_id: 'p9',
    tags: [{ id: 't1', name: 'rpg' }],
  })
  const { onSave } = renderForm(entry)
  const notes = screen.getByLabelText(/notes/i)
  await userEvent.clear(notes)
  await userEvent.type(notes, 'replayed in 2026')
  await userEvent.click(screen.getByRole('button', { name: /save/i }))
  expect(onSave).toHaveBeenCalledTimes(1)
  const sent = onSave.mock.calls[0][0] as EntryUpdate
  expect(sent.notes).toBe('replayed in 2026')
  // Untouched fields survive: the pricing draft initializes from the
  // entry and rides the payload unchanged, and tags survive.
  expect(sent.pricing_mode).toBe('proxy')
  expect(sent.pricing_product_id).toBe('p9')
  expect(sent.tag_ids).toEqual(['t1'])
  expect(sent.price_paid_cents).toBe(1500)
  expect(sent.rating).toBe(7)
})

it('clearing an optional input drops the field from the payload', async () => {
  const { onSave } = renderForm(entryFixture({ notes: 'to be removed' }))
  await userEvent.clear(screen.getByLabelText(/notes/i))
  await userEvent.click(screen.getByRole('button', { name: /save/i }))
  const sent = onSave.mock.calls[0][0] as EntryUpdate
  expect(sent.notes).toBeUndefined()
})

it('drafts pricing edits for the save button: the radio moves, Saved. retracts, the payload carries them', async () => {
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } })
  vi.stubGlobal('fetch', vi.fn().mockResolvedValue(jsonResponse(200, { tags: [] })))
  const onSave = vi.fn()
  render(
    <QueryClientProvider client={qc}>
      <EntryForm
        entry={entryFixture({ pricing_mode: 'proxy', pricing_product_id: 'p9' })}
        onSave={onSave} saving={false} saved={true} error={null}
      />
    </QueryClientProvider>,
  )
  expect(screen.getByText('Saved.')).toBeInTheDocument()
  await userEvent.click(screen.getByRole('radio', { name: /disabled/i }))
  expect(screen.getByRole('radio', { name: /disabled/i })).toBeChecked()
  expect(screen.queryByText('Saved.')).not.toBeInTheDocument()
  expect(onSave).not.toHaveBeenCalled()
  await userEvent.click(screen.getByRole('button', { name: /save/i }))
  const sent = onSave.mock.calls[0][0] as EntryUpdate
  expect(sent.pricing_mode).toBe('disabled')
  expect(sent.pricing_product_id).toBe('p9')
})

it('blocks saving a proxy that has no price source yet', async () => {
  const { onSave } = renderForm(
    entryFixture({ product_id: undefined, display_name: 'Repro Cart', pricing_mode: 'disabled', pricing_product_id: undefined }),
  )
  await userEvent.click(screen.getByRole('radio', { name: /proxy/i }))
  await userEvent.click(await screen.findByRole('button', { name: 'Close' }))
  await userEvent.click(screen.getByRole('button', { name: /save/i }))
  expect(onSave).not.toHaveBeenCalled()
  expect(screen.getByRole('alert')).toHaveTextContent('Choose a price source before saving.')
})

it('clears box and manual when packaging goes loose', async () => {
  const { onSave } = renderForm(
    entryFixture({ has_box: true, has_manual: true, box_condition: 'good', manual_condition: 'good' }),
  )
  await userEvent.selectOptions(screen.getByLabelText(/^packaging/i), 'loose')
  expect(screen.getByRole('checkbox', { name: /has box/i })).not.toBeChecked()
  expect(screen.getByRole('checkbox', { name: /has manual/i })).not.toBeChecked()
  expect(screen.queryByLabelText(/box condition/i)).not.toBeInTheDocument()
  await userEvent.click(screen.getByRole('button', { name: /save/i }))
  const sent = onSave.mock.calls[0][0] as EntryUpdate
  expect(sent.packaging).toBe('loose')
  expect(sent.has_box).toBe(false)
  expect(sent.has_manual).toBe(false)
  expect(sent.box_condition).toBeUndefined()
  expect(sent.manual_condition).toBeUndefined()
})

it('checks box and manual when packaging goes cib or sealed', async () => {
  const { onSave } = renderForm(
    entryFixture({ packaging: 'loose', has_box: false, has_manual: false, box_condition: undefined, manual_condition: undefined }),
  )
  await userEvent.selectOptions(screen.getByLabelText(/^packaging/i), 'cib')
  expect(screen.getByRole('checkbox', { name: /has box/i })).toBeChecked()
  expect(screen.getByRole('checkbox', { name: /has manual/i })).toBeChecked()
  expect(screen.getByLabelText(/box condition/i)).toBeInTheDocument()
  await userEvent.click(screen.getByRole('button', { name: /save/i }))
  const sent = onSave.mock.calls[0][0] as EntryUpdate
  expect(sent.packaging).toBe('cib')
  expect(sent.has_box).toBe(true)
  expect(sent.has_manual).toBe(true)
})

it('renders the save error', () => {
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } })
  vi.stubGlobal('fetch', vi.fn().mockResolvedValue(jsonResponse(200, { tags: [] })))
  render(
    <QueryClientProvider client={qc}>
      <EntryForm entry={entryFixture()} onSave={vi.fn()} saving={false} saved={false} error="no such pricing product" />
    </QueryClientProvider>,
  )
  expect(screen.getByRole('alert')).toHaveTextContent('no such pricing product')
})

// Not brief-specified: the fields above cover the payload semantics the
// brief calls out (full-replacement baseline, clearing, gating). This
// test exercises every remaining field's own control once so the
// coverage gate holds; it is additional breadth, not new behavior.
it('carries edits from every remaining field control into the payload', async () => {
  const tags = [{ id: 't1', name: 'rpg', entry_count: 1 }]
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } })
  vi.stubGlobal('fetch', vi.fn().mockResolvedValue(jsonResponse(200, { tags })))
  const onSave = vi.fn()
  const entry = entryFixture({ product_id: undefined, display_name: 'Repro Cart' })
  render(
    <QueryClientProvider client={qc}>
      <EntryForm entry={entry} onSave={onSave} saving={false} saved={false} error={null} />
    </QueryClientProvider>,
  )
  await userEvent.click(await screen.findByRole('checkbox', { name: /rpg/i }))

  await userEvent.clear(screen.getByLabelText(/^name$/i))
  await userEvent.type(screen.getByLabelText(/^name$/i), 'Repro Cart II')
  await userEvent.clear(screen.getByLabelText(/^platform$/i))
  await userEvent.type(screen.getByLabelText(/^platform$/i), 'Famicom')
  fireEvent.change(screen.getByLabelText(/release date/i), { target: { value: '1999-12-31' } })
  await userEvent.selectOptions(screen.getByLabelText(/^region/i), 'pal')
  await userEvent.type(screen.getByLabelText(/^edition$/i), 'black label')
  await userEvent.selectOptions(screen.getByLabelText(/^packaging/i), 'loose')
  await userEvent.selectOptions(screen.getByLabelText(/item condition/i), 'poor')
  await userEvent.click(screen.getByRole('checkbox', { name: /has manual/i }))
  await userEvent.type(screen.getByLabelText(/price paid/i), '9.99')
  await userEvent.clear(screen.getByLabelText(/^currency$/i))
  await userEvent.type(screen.getByLabelText(/^currency$/i), 'eur')
  fireEvent.change(screen.getByLabelText(/purchased on/i), { target: { value: '2020-05-01' } })
  await userEvent.type(screen.getByLabelText(/purchased from/i), 'a friend')
  await userEvent.selectOptions(screen.getByLabelText(/^status/i), 'playing')
  await userEvent.selectOptions(screen.getByLabelText(/^rating/i), '5')
  await userEvent.type(screen.getByLabelText(/storage location/i), 'shelf 3')
  await userEvent.click(screen.getByRole('checkbox', { name: /^pinned$/i }))

  await userEvent.click(screen.getByRole('button', { name: /save/i }))
  expect(onSave).toHaveBeenCalledTimes(1)
  const sent = onSave.mock.calls[0][0] as EntryUpdate
  expect(sent.display_name).toBe('Repro Cart II')
  expect(sent.platform_name).toBe('Famicom')
  expect(sent.first_release_date).toBe('1999-12-31')
  expect(sent.region).toBe('pal')
  expect(sent.edition).toBe('black label')
  expect(sent.packaging).toBe('loose')
  expect(sent.item_condition).toBe('poor')
  // Going loose cleared both flags; the manual click above re-checked
  // manual only.
  expect(sent.has_box).toBe(false)
  expect(sent.has_manual).toBe(true)
  expect(sent.price_paid_cents).toBe(999)
  expect(sent.currency).toBe('EUR')
  expect(sent.purchased_at).toBe('2020-05-01')
  expect(sent.purchased_from).toBe('a friend')
  expect(sent.status).toBe('playing')
  expect(sent.rating).toBe(5)
  expect(sent.storage_location).toBe('shelf 3')
  expect(sent.pinned).toBe(true)
  expect(sent.tag_ids).toEqual(['t1'])
})

it('carries a stored custom price through an untouched save', async () => {
  const { onSave } = renderForm(entryFixture({ pricing_mode: 'custom', custom_value_cents: 12345 }))
  await userEvent.click(screen.getByRole('button', { name: /save/i }))
  expect(onSave).toHaveBeenCalledTimes(1)
  const sent = onSave.mock.calls[0][0] as EntryUpdate
  expect(sent.custom_value_cents).toBe(12345)
  expect(sent.pricing_mode).toBe('custom')
})

it('blocks saving a custom price left empty', async () => {
  // pricing_product_id stays undefined so PricingPanel fires no product
  // fetch of its own here: it would otherwise race TagPicker's tags
  // fetch for the single mocked response body (see renderForm) and
  // could surface a second, unrelated role=alert.
  const { onSave } = renderForm(entryFixture({ pricing_mode: 'disabled', pricing_product_id: undefined }))
  await userEvent.click(screen.getByRole('radio', { name: /^custom/i }))
  await userEvent.click(screen.getByRole('button', { name: /save/i }))
  expect(onSave).not.toHaveBeenCalled()
  expect(screen.getByRole('alert')).toHaveTextContent('Enter a custom price before saving.')
})

it('keeps a stored custom price in the payload when saving under a different mode', async () => {
  const { onSave } = renderForm(
    entryFixture({ pricing_mode: 'proxy', pricing_product_id: 'p9', custom_value_cents: 1200 }),
  )
  await userEvent.click(screen.getByRole('button', { name: /save/i }))
  const sent = onSave.mock.calls[0][0] as EntryUpdate
  expect(sent.custom_value_cents).toBe(1200)
  expect(sent.pricing_mode).toBe('proxy')
})
