import type { Entry, EntryList } from '../api/collection'

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

export const jsonResponse = (status: number, body: unknown) =>
  new Response(JSON.stringify(body), {
    status,
    headers: { 'Content-Type': 'application/json' },
  })
