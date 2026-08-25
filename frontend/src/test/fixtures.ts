import type { Me } from '../api/me'
import type { Dashboard, Entry, EntryList } from '../api/collection'
import type { FXRates } from '../api/fx'
import type { SharedEntry } from '../components/collection/rowMeta'

let seq = 0

// Minimal valid product-backed Entry; override what the test cares
// about (product_id: undefined makes it custom).
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

// Minimal valid SharedEntry, the cross-user whitelist projection of
// Entry; deliberately has no status/rating/price fields.
export function sharedEntryFixture(overrides: Partial<SharedEntry> = {}): SharedEntry {
  seq++
  const n = String(seq).padStart(12, '0')
  return {
    id: `00000000-0000-0000-0000-${n}`,
    item_type: 'game',
    media_type: 'physical',
    display_name: `Shared Game ${seq}`,
    platform: { igdb_platform_id: 6, name: 'SNES' },
    region: 'ntsc_u',
    packaging: 'cib',
    has_box: true,
    has_manual: true,
    pinned: false,
    tags: [],
    created_at: '2026-07-01T00:00:00Z',
    ...overrides,
  }
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

export function meFixture(overrides: Partial<Me> = {}): Me {
  return {
    id: '99999999-0000-0000-0000-000000000001',
    email: 'alice@example.com',
    handle: 'Alice',
    roles: ['user'],
    preferred_currency: 'USD',
    profile_visibility: 'private',
    landing_page: 'feed',
    ...overrides,
  }
}

// Round rates on purpose: $42.00 is exactly 21 EUR, 31.50 GBP, 6300 JPY.
// date defaults to today, so a default render is never flagged stale;
// pass an override to test the stale-snapshot cue.
export function fxRatesFixture(overrides: Partial<FXRates> = {}): FXRates {
  return {
    base: 'USD',
    date: new Date().toISOString().slice(0, 10),
    rates: { EUR: 0.5, GBP: 0.75, JPY: 150 },
    ...overrides,
  }
}

export const jsonResponse = (status: number, body: unknown) =>
  new Response(JSON.stringify(body), {
    status,
    headers: { 'Content-Type': 'application/json' },
  })

// RFC 9457 problem+json Response. type/title are fixed placeholders
// (client.ts only reads status/code/detail); JSON.stringify drops
// undefined keys, so omitted code/detail are simply absent.
export const problemResponse = (status: number, code?: string, detail?: string) =>
  new Response(JSON.stringify({ type: 'about:blank', title: 'x', status, code, detail }), {
    status,
    headers: { 'Content-Type': 'application/problem+json' },
  })

// Parses the JSON body off a mocked fetch call's Request; clones
// before reading since a body is consumed once and may be parsed twice.
export async function putBody<T = unknown>(input: unknown): Promise<T> {
  return (await (input as Request).clone().json()) as T
}

// Reads the wire path+query off a stubbed fetch call (string URL or Request).
export function calledPath(fetchMock: { mock: { calls: unknown[][] } }, n?: number): string {
  const calls = fetchMock.mock.calls
  const input = calls[n ?? calls.length - 1][0]
  const url = input instanceof Request ? input.url : String(input)
  const u = new URL(url, 'http://test.local')
  return u.pathname + u.search
}

// calledPath's router-side twin: normalizes one fetch argument to
// path+query, for mockImplementation branching.
export function requestPath(input: unknown): string {
  const url = input instanceof Request ? input.url : String(input)
  const u = new URL(url, 'http://test.local')
  return u.pathname + u.search
}
