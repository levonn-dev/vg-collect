import { act, fireEvent, screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import type { EntryUpdate } from '../../api/collection'
import { entryFixture, fxRatesFixture, jsonResponse, meFixture } from '../../test/fixtures'
import { renderWithMoney } from '../../test/money'
import EntryForm from './EntryForm'

function renderForm(
  entry = entryFixture(),
  onSave = vi.fn(),
  moneyOpts: { currency?: string; rates?: boolean } = {},
) {
  // mockImplementation, URL-aware, fresh Response per call: a custom
  // entry mounts several concurrent fetchers (TagPicker, PlatformPicker,
  // and - once a price-source picker opens - SearchPicker's auto-fired
  // search), each needing its own shape and its own Response instance (a
  // shared instance can only have its body read once).
  vi.stubGlobal('fetch', vi.fn().mockImplementation((url: string) => {
    const u = String(url)
    if (u.startsWith('/api/search')) return Promise.resolve(jsonResponse(200, { degraded: false, results: [] }))
    if (u === '/api/platforms') return Promise.resolve(jsonResponse(200, { platforms: [] }))
    return Promise.resolve(jsonResponse(200, { tags: [] }))
  }))
  const view = renderWithMoney(
    <EntryForm entry={entry} onSave={onSave} saving={false} saved={false} error={null} />,
    moneyOpts,
  )
  return { onSave, unmount: view.unmount, queryClient: view.queryClient }
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
  vi.stubGlobal('fetch', vi.fn().mockResolvedValue(jsonResponse(200, { tags: [] })))
  const onSave = vi.fn()
  renderWithMoney(
    <EntryForm
      entry={entryFixture({ pricing_mode: 'proxy', pricing_product_id: 'p9' })}
      onSave={onSave} saving={false} saved={true} error={null}
    />,
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
  vi.stubGlobal('fetch', vi.fn().mockResolvedValue(jsonResponse(200, { tags: [] })))
  renderWithMoney(
    <EntryForm entry={entryFixture()} onSave={vi.fn()} saving={false} saved={false} error="no such pricing product" />,
  )
  expect(screen.getByRole('alert')).toHaveTextContent('no such pricing product')
})

// Price paid is stamped once at create and never re-currencied by an
// edit: the label shows the entry's own stored currency (JPY) even
// though the signed-in profile displays in a different one (EUR), and
// the save payload must carry the same stored code through unchanged.
it('preserves the stored paid currency on edit and shows it on the label', async () => {
  const entry = entryFixture({ currency: 'JPY', price_paid_cents: 500000 })
  const { onSave } = renderForm(entry, vi.fn(), { currency: 'EUR' })

  expect(screen.queryByLabelText(/^currency$/i)).not.toBeInTheDocument()
  expect(screen.getByText(/price paid \(jpy\)/i)).toBeInTheDocument()

  await userEvent.click(screen.getByRole('button', { name: /save/i }))
  const sent = onSave.mock.calls[0][0] as EntryUpdate
  expect(sent.currency).toBe('JPY')
})

// Not brief-specified: the fields above cover the payload semantics the
// brief calls out (full-replacement baseline, clearing, gating). This
// test exercises every remaining field's own control once so the
// coverage gate holds; it is additional breadth, not new behavior.
it('carries edits from every remaining field control into the payload', async () => {
  const tags = [{ id: 't1', name: 'rpg', entry_count: 1 }]
  const platforms = { platforms: [{ igdb_id: 99, name: 'Famicom', aliases: [] }] }
  vi.stubGlobal('fetch', vi.fn().mockImplementation((url: string) => {
    if (String(url) === '/api/platforms') return Promise.resolve(jsonResponse(200, platforms))
    return Promise.resolve(jsonResponse(200, { tags }))
  }))
  const onSave = vi.fn()
  const entry = entryFixture({ product_id: undefined, display_name: 'Repro Cart' })
  renderWithMoney(<EntryForm entry={entry} onSave={onSave} saving={false} saved={false} error={null} />)
  await userEvent.click(await screen.findByRole('checkbox', { name: /rpg/i }))

  await userEvent.clear(screen.getByLabelText(/^name$/i))
  await userEvent.type(screen.getByLabelText(/^name$/i), 'Repro Cart II')
  // The fixture's platform already carries a canonical id, so the picker
  // opens confirmed (see PlatformPicker's value-driven confirmed state):
  // Change is required before the input exists to type into.
  await userEvent.click(screen.getByRole('button', { name: 'Change platform' }))
  await userEvent.type(screen.getByLabelText(/^platform$/i), 'Famicom')
  await userEvent.click(await screen.findByRole('button', { name: 'Famicom' }))
  fireEvent.change(screen.getByLabelText(/release date/i), { target: { value: '1999-12-31' } })
  await userEvent.type(screen.getByLabelText(/cover image link/i), 'https://img.example/cover.jpg')
  await userEvent.selectOptions(screen.getByLabelText(/^region/i), 'pal')
  await userEvent.type(screen.getByLabelText(/^edition$/i), 'black label')
  await userEvent.selectOptions(screen.getByLabelText(/^packaging/i), 'loose')
  await userEvent.selectOptions(screen.getByLabelText(/item condition/i), 'poor')
  await userEvent.click(screen.getByRole('checkbox', { name: /has manual/i }))
  await userEvent.type(screen.getByLabelText(/price paid/i), '9.99')
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
  expect(sent.platform_igdb_id).toBe(99)
  expect(sent.first_release_date).toBe('1999-12-31')
  expect(sent.cover_url).toBe('https://img.example/cover.jpg')
  expect(sent.region).toBe('pal')
  expect(sent.edition).toBe('black label')
  expect(sent.packaging).toBe('loose')
  expect(sent.item_condition).toBe('poor')
  // Going loose cleared both flags; the manual click above re-checked
  // manual only.
  expect(sent.has_box).toBe(false)
  expect(sent.has_manual).toBe(true)
  expect(sent.price_paid_cents).toBe(999)
  expect(sent.purchased_at).toBe('2020-05-01')
  expect(sent.purchased_from).toBe('a friend')
  expect(sent.status).toBe('playing')
  expect(sent.rating).toBe(5)
  expect(sent.storage_location).toBe('shelf 3')
  expect(sent.pinned).toBe(true)
  expect(sent.tag_ids).toEqual(['t1'])
})

it('omits cover_url from the payload when the cover input is left empty', async () => {
  const { onSave } = renderForm(entryFixture({ product_id: undefined, display_name: 'Repro Cart' }))
  await screen.findByLabelText(/cover image link/i)
  await userEvent.click(screen.getByRole('button', { name: /save/i }))
  expect(onSave).toHaveBeenCalledTimes(1)
  const sent = onSave.mock.calls[0][0] as EntryUpdate
  // Value-level: the field carries no cover. Wire-level: cover_url must
  // be absent from the serialized body, not present-but-undefined - the
  // two are indistinguishable on the raw object (toUpdate always
  // assigns the key) but not after JSON.stringify, which is what
  // actually crosses the network (same wire round-trip idiom as
  // lib/entryUpdate.test.ts).
  expect(sent.cover_url).toBeUndefined()
  const wire = JSON.parse(JSON.stringify(sent)) as Record<string, unknown>
  expect(wire).not.toHaveProperty('cover_url')
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

it('saves a custom price typed in the display currency as pair plus USD snapshot', async () => {
  const entry = entryFixture({ pricing_mode: 'custom', custom_value_cents: 5400 })
  const { onSave } = renderForm(entry, vi.fn(), { currency: 'EUR' })

  const input = screen.getByLabelText(/custom price \(eur\)/i)
  await userEvent.clear(input)
  await userEvent.type(input, '80')
  await userEvent.click(screen.getByRole('button', { name: /save/i }))

  const sent = onSave.mock.calls[0][0] as EntryUpdate
  expect(sent.custom_value_entered_cents).toBe(8000)
  expect(sent.custom_value_entered_currency).toBe('EUR')
  expect(sent.custom_value_cents).toBe(16000) // 8000 / 0.5
})

it('prefills the input from a matching stored pair, verbatim', () => {
  const entry = entryFixture({
    pricing_mode: 'custom',
    custom_value_cents: 11900,
    custom_value_entered_cents: 6000,
    custom_value_entered_currency: 'EUR',
  })
  renderForm(entry, vi.fn(), { currency: 'EUR' })
  expect(screen.getByLabelText(/custom price \(eur\)/i)).toHaveValue('60.00')
})

it('falls back to a USD input when rates are unavailable', () => {
  const entry = entryFixture({ pricing_mode: 'custom', custom_value_cents: 5400 })
  renderForm(entry, vi.fn(), { currency: 'EUR', rates: false })
  expect(screen.getByLabelText(/custom price \(usd\)/i)).toHaveValue('54.00')
})

it('blocks saving a custom price when the rate vanishes after mount', async () => {
  // The input currency froze to EUR at mount; a fresh snapshot then
  // arrives WITHOUT a EUR rate. Replacing the cached snapshot with a
  // defined object updates observers synchronously and stays fresh
  // (staleTime Infinity), so no refetch races the assertion.
  const entry = entryFixture({ pricing_mode: 'custom', custom_value_cents: 5400 })
  const { onSave, queryClient } = renderForm(entry, vi.fn(), { currency: 'EUR' })
  expect(screen.getByLabelText(/custom price \(eur\)/i)).toBeInTheDocument()
  await act(() => queryClient.setQueryData(['fx'], fxRatesFixture({ rates: { GBP: 0.75 } })))
  await userEvent.click(screen.getByRole('button', { name: /save/i }))
  expect(onSave).not.toHaveBeenCalled()
  expect(screen.getByRole('alert')).toHaveTextContent('Exchange rates are unavailable; try saving again shortly.')
})

it('converts the draft at the frozen input currency when the display currency changes mid-edit', async () => {
  // The input currency froze to EUR at mount. The header selector then
  // flips the profile to JPY mid-edit (optimistic ['me'] update, no
  // remount) - the draft typed in EUR must still convert at EUR's
  // rate, not JPY's.
  const entry = entryFixture({ pricing_mode: 'custom', custom_value_cents: 5400 })
  const { onSave, queryClient } = renderForm(entry, vi.fn(), { currency: 'EUR' })
  const input = screen.getByLabelText(/custom price \(eur\)/i)
  expect(input).toBeInTheDocument()
  await userEvent.clear(input)
  await userEvent.type(input, '60')
  await act(() => queryClient.setQueryData(['me'], meFixture({ preferred_currency: 'JPY' })))
  await userEvent.click(screen.getByRole('button', { name: /save/i }))
  const sent = onSave.mock.calls[0][0] as EntryUpdate
  expect(sent.custom_value_cents).toBe(12000)
  expect(sent.custom_value_entered_cents).toBe(6000)
  expect(sent.custom_value_entered_currency).toBe('EUR')
})

// Not brief-specified: locks in the deliberate behavior change named in the
// design (a blanked draft no longer clears stored memory on save) - without
// this, a stray clear-then-switch-mode click would silently erase
// custom_value_cents from the baseline echo.
it('preserves a stored custom price when the draft is blanked and saved under a different mode', async () => {
  const entry = entryFixture({
    pricing_mode: 'custom',
    custom_value_cents: 1200,
    custom_value_entered_cents: 1200,
    custom_value_entered_currency: 'USD',
  })
  const { onSave } = renderForm(entry)
  await userEvent.clear(screen.getByLabelText(/custom price \(usd\)/i))
  await userEvent.click(screen.getByRole('radio', { name: /disabled/i }))
  await userEvent.click(screen.getByRole('button', { name: /save/i }))
  const sent = onSave.mock.calls[0][0] as EntryUpdate
  expect(sent.custom_value_cents).toBe(1200)
  expect(sent.custom_value_entered_cents).toBe(1200)
  expect(sent.custom_value_entered_currency).toBe('USD')
})
