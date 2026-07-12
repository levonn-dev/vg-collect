import type { Entry, EntryUpdate } from '../api/collection'

// entryToUpdate builds the full-replacement PUT payload from an entry.
// The update contract replaces ALL mutable state (an absent optional
// field is cleared), so every mutation - a pin toggle, a pricing-mode
// switch - starts from this faithful baseline and overrides one field.
// Custom entries (no product_id) additionally own their display
// fields; on product-backed entries those must not be sent.
export function entryToUpdate(e: Entry): EntryUpdate {
  const u: EntryUpdate = {
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
    u.display_name = e.display_name
    u.platform_name = e.platform?.name
    u.first_release_date = e.first_release_date
  }
  return u
}
