import type { Entry } from '../api/collection'
import { entryToUpdate } from './entryUpdate'

const base: Entry = {
  id: 'e1', item_type: 'game', media_type: 'physical', display_name: 'Chrono Trigger',
  region: 'ntsc_u', packaging: 'cib', has_box: true, has_manual: false,
  box_condition: 'very_good', currency: 'USD', pricing_mode: 'auto',
  status: 'beaten', rating: 9, notes: 'first print', pinned: true,
  source: 'manual', tags: [{ id: 't1', name: 'rpg' }, { id: 't2', name: 'snes' }],
  created_at: '2026-07-01T00:00:00Z', updated_at: '2026-07-01T00:00:00Z',
}

it('carries every mutable field and the tag ids', () => {
  const u = entryToUpdate({ ...base, product_id: 'p1', pricing_product_id: 'p2' })
  expect(u).toMatchObject({
    region: 'ntsc_u', packaging: 'cib', has_box: true, has_manual: false,
    box_condition: 'very_good', pricing_mode: 'auto', pricing_product_id: 'p2',
    status: 'beaten', rating: 9, notes: 'first print', pinned: true,
    tag_ids: ['t1', 't2'],
  })
})

it('omits display fields on product-backed entries (the server rejects them)', () => {
  const u = entryToUpdate({ ...base, product_id: 'p1' })
  expect('display_name' in u).toBe(false)
  expect('platform_name' in u).toBe(false)
  expect('first_release_date' in u).toBe(false)
})

it('carries display fields on custom entries (they are user-owned)', () => {
  const u = entryToUpdate({
    ...base, product_id: undefined,
    platform: { name: 'SNES' }, first_release_date: '1995-03-11',
  })
  expect(u.display_name).toBe('Chrono Trigger')
  expect(u.platform_name).toBe('SNES')
  expect(u.first_release_date).toBe('1995-03-11')
})

it('carries the linked platform, cover image, and credits on custom entries (a baseline spread must not clear them)', () => {
  const u = entryToUpdate({
    ...base, product_id: undefined,
    platform: { name: 'SNES', igdb_platform_id: 19 },
    cover_url: 'https://example.com/cover.jpg',
    developers: ['Square'],
    publishers: ['Square'],
  })
  expect(u.platform_igdb_id).toBe(19)
  expect(u.cover_url).toBe('https://example.com/cover.jpg')
  expect(u.developers).toEqual(['Square'])
  expect(u.publishers).toEqual(['Square'])
})

it('a serialized baseline drops no set field (absent means cleared on PUT)', () => {
  const u = entryToUpdate({ ...base, product_id: 'p1' })
  const wire = JSON.parse(JSON.stringify(u)) as Record<string, unknown>
  expect(wire.box_condition).toBe('very_good')
  expect(wire.rating).toBe(9)
  // Unset optionals serialize to absent, which the PUT reads as
  // "cleared" - exactly the entry's current state for those fields.
  expect('manual_condition' in wire).toBe(false)
  expect('purchased_at' in wire).toBe(false)
})

// Guards the baseline itself: a spread-only mutation (PinStar's pin
// toggle) must not silently drop a stored custom price.
it('carries the custom price pair so an unrelated mutation cannot erase it', () => {
  const u = entryToUpdate({ ...base, product_id: 'p1', pricing_mode: 'custom', custom_value_cents: 1200 })
  expect(u.custom_value_cents).toBe(1200)
})

it('echoes the stored entered pair in the full-replacement baseline', () => {
  const u = entryToUpdate({
    ...base, product_id: 'p1', pricing_mode: 'custom',
    custom_value_cents: 12000, custom_value_entered_cents: 6000, custom_value_entered_currency: 'EUR',
  })
  expect(u.custom_value_entered_cents).toBe(6000)
  expect(u.custom_value_entered_currency).toBe('EUR')
})
