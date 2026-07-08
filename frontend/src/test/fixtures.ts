import type { Dashboard, Entry, EntryList } from '../api/collection'

let seq = 0

// entryFixture builds a minimal valid product-backed Entry; override
// what the test cares about (product_id: undefined makes it custom).
export function entryFixture(overrides: Partial<Entry> = {}): Entry {
  seq++
  const n = String(seq).padStart(12, '0')
  return {
    id: `00000000-0000-0000-0000-${n}`,
    product_id: `11111111-0000-0000-0000-${n}`,
    item_type: 'game',
    media_type: 'physical',
    display_name: `Game ${seq}`,
    platform: { igdb_platform_id: 6, name: 'SNES' },
    region: 'ntsc_u',
    packaging: 'cib',
    has_box: true,
    has_manual: true,
    currency: 'USD',
    pricing_mode: 'auto',
    status: 'backlog',
    pinned: false,
    source: 'manual',
    tags: [],
    created_at: '2026-07-01T00:00:00Z',
    updated_at: '2026-07-01T00:00:00Z',
    ...overrides,
  }
}

export function listFixture(entries: Entry[], overrides: Partial<EntryList> = {}): EntryList {
  return { pricing_available: true, total_count: entries.length, entries, ...overrides }
}

export function dashboardFixture(overrides: Partial<Dashboard> = {}): Dashboard {
  return {
    total_entries: 42,
    by_status: { backlog: 12, beaten: 20, playing: 3 },
    by_item_type: { game: 38, console: 3, accessory: 1 },
    by_platform: [
      { name: 'SNES', count: 21 },
      { name: 'PlayStation', count: 14 },
    ],
    spend: [{ currency: 'USD', total_cents: 210000 }],
    pricing: {
      available: true, total_value_cents: 384200,
      priced_entries: 35, unpriced_entries: 4, excluded_entries: 3,
    },
    ...overrides,
  }
}

export const jsonResponse = (status: number, body: unknown) =>
  new Response(JSON.stringify(body), {
    status,
    headers: { 'Content-Type': 'application/json' },
  })
