import type { APIRequestContext } from '@playwright/test'
import { expect } from '@playwright/test'

// Test arranges go through the same /api/* surface the app uses: a
// handful of requests instead of a click path, which keeps the suite
// far under the gateway's shared request budget. Every helper asserts
// success loudly - a silent failed arrange turns into a confusing
// assertion failure later.

type EntryFields = {
  // Required for a custom (no product_id) entry; the server rejects it
  // outright alongside product_id (catalog fields come from the
  // resolved product instead - validateCustomFields in the collection
  // service's handlers_entries.go).
  display_name?: string
  item_type?: 'game' | 'console'
  region?: string
  packaging?: string
  status?: string
  rating?: number
  notes?: string
  product_id?: string
  platform_name?: string
  platform_igdb_id?: number
  developers?: string[]
  publishers?: string[]
  currency?: string
}

export async function createEntry(api: APIRequestContext, fields: EntryFields): Promise<{ id: string; url: string }> {
  // item_type is another catalog field the server rejects alongside
  // product_id (same rule as display_name above), so the custom-entry
  // default only applies when the caller is not resolving a product.
  const defaults: Record<string, unknown> = fields.product_id ? {} : { item_type: 'game' }
  const res = await api.post('/api/entries', {
    data: { ...defaults, region: 'ntsc_u', packaging: 'loose', ...fields },
  })
  expect(res.ok(), `create entry ${fields.display_name ?? fields.product_id}: ${res.status()}`).toBeTruthy()
  const { id } = (await res.json()) as { id: string }
  return { id, url: `/entries/${id}` }
}

export async function updateEntry(api: APIRequestContext, id: string, fields: Record<string, unknown>) {
  // PUT is full-replacement in this API; PATCH-style helpers must
  // read-modify-write. Check the facade for which the UI uses and
  // mirror it exactly.
  const current = await api.get(`/api/entries/${id}`)
  expect(current.ok(), `read entry ${id}: ${current.status()}`).toBeTruthy()
  const e = (await current.json()) as {
    product_id?: string
    display_name: string
    platform?: { name?: string }
    first_release_date?: string
    tags: { id: string }[]
    [key: string]: unknown
  }
  // Mirrors entryToUpdate in frontend/src/lib/entryUpdate.ts: the PUT
  // contract replaces all mutable state, so an absent optional field
  // is cleared - every mutation starts from this faithful baseline.
  const base: Record<string, unknown> = {
    region: e.region,
    edition: e.edition,
    packaging: e.packaging,
    has_box: e.has_box,
    has_manual: e.has_manual,
    box_condition: e.box_condition,
    manual_condition: e.manual_condition,
    item_condition: e.item_condition,
    price_paid_cents: e.price_paid_cents,
    currency: e.currency,
    purchased_at: e.purchased_at,
    purchased_from: e.purchased_from,
    pricing_mode: e.pricing_mode,
    pricing_product_id: e.pricing_product_id,
    custom_value_cents: e.custom_value_cents,
    custom_value_entered_cents: e.custom_value_entered_cents,
    custom_value_entered_currency: e.custom_value_entered_currency,
    status: e.status,
    rating: e.rating,
    notes: e.notes,
    storage_location: e.storage_location,
    pinned: e.pinned,
    tag_ids: e.tags.map((t) => t.id),
  }
  if (!e.product_id) {
    base.display_name = e.display_name
    base.platform_name = e.platform?.name
    base.first_release_date = e.first_release_date
  }
  const res = await api.put(`/api/entries/${id}`, { data: { ...base, ...fields } })
  expect(res.ok(), `update entry ${id}: ${res.status()}`).toBeTruthy()
}

export async function deleteEntry(api: APIRequestContext, id: string) {
  const res = await api.delete(`/api/entries/${id}`)
  expect(res.ok(), `delete entry ${id}: ${res.status()}`).toBeTruthy()
}

export async function createTag(api: APIRequestContext, name: string): Promise<string> {
  const res = await api.post('/api/tags', { data: { name } })
  expect(res.ok(), `create tag ${name}: ${res.status()}`).toBeTruthy()
  const { id } = (await res.json()) as { id: string }
  return id
}

export async function deleteTag(api: APIRequestContext, id: string) {
  const res = await api.delete(`/api/tags/${id}`)
  expect(res.ok(), `delete tag ${id}: ${res.status()}`).toBeTruthy()
}

export async function listViews(api: APIRequestContext) {
  const res = await api.get('/api/views')
  expect(res.ok(), `list views: ${res.status()}`).toBeTruthy()
  const body = (await res.json()) as {
    views: { id: string; name: string; slug: string; visibility: string; params: Record<string, unknown> }[]
  }
  return body.views
}

export async function setViewVisibility(
  api: APIRequestContext,
  id: string,
  name: string,
  params: Record<string, unknown>,
  visibility: string,
) {
  // PUT /api/views/{id} full replacement (name, params, visibility) -
  // mirrors updateView in frontend/src/api/collection.ts.
  const res = await api.put(`/api/views/${id}`, { data: { name, params, visibility } })
  expect(res.ok(), `update view ${id}: ${res.status()}`).toBeTruthy()
}

export async function setProfile(api: APIRequestContext, patch: { profile_visibility?: string; handle?: string }) {
  const res = await api.patch('/api/me', { data: patch })
  expect(res.ok(), `patch /api/me: ${res.status()}`).toBeTruthy()
}

export async function submitEntry(api: APIRequestContext, entryId: string): Promise<string> {
  const res = await api.post(`/api/entries/${entryId}/submission`)
  expect(res.ok(), `submit entry ${entryId}: ${res.status()}`).toBeTruthy()
  const { id } = (await res.json()) as { id: string }
  return id
}

// Minimal slice of CommunityProductSpec (frontend/src/api/schema.ts) -
// only the fields the one approve_new call site below actually sends;
// the full schema type carries several more optional catalog fields
// no test here needs.
type CommunityProduct = {
  type: 'game' | 'console'
  name: string
  platform_name?: string
}

type VerdictRequest =
  | { action: 'approve_new'; product: CommunityProduct }
  | { action: 'approve_existing'; product_id: string }
  | { action: 'reject'; reason: string }

// POST /api/admin/submissions/{id}/verdict. Body shape mirrors
// VerdictRequest in frontend/src/api/admin.ts, exercised in
// ReviewPanel.tsx's approve-as-new, adopt-existing, and reject calls:
// approve_new carries product, approve_existing carries product_id,
// reject carries reason.
export async function reviewSubmission(adminApi: APIRequestContext, submissionId: string, verdict: VerdictRequest) {
  const res = await adminApi.post(`/api/admin/submissions/${submissionId}/verdict`, { data: verdict })
  expect(res.ok(), `verdict on submission ${submissionId}: ${res.status()}`).toBeTruthy()
}

export async function resolveProduct(api: APIRequestContext, payload: Record<string, unknown>): Promise<{ id: string; name: string }> {
  // POST /api/products/resolve. Callers pass the exact payload the add
  // wizard would send; copy the shape from resolveRequestFor in
  // frontend/src/lib/catalog.ts.
  const res = await api.post('/api/products/resolve', { data: payload })
  expect(res.ok(), `resolve product: ${res.status()}`).toBeTruthy()
  return (await res.json()) as { id: string; name: string }
}
