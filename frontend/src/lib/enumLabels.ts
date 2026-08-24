import { msg } from '@lingui/core/macro'
import type { MessageDescriptor } from '@lingui/core'
import type { Product } from '../api/catalog'
import type { SavedView } from '../api/collection'
import type { Condition, ItemType, Packaging, Status } from './listParams'

// Each map is typed as Record<Enum, MessageDescriptor> against the
// listParams enum types, so a future enum member fails to compile at
// the map that is missing it, instead of quietly rendering blank.

// statusLabels: the Title-case form (EntryTable's status column,
// CompactList's status span, BulkEditBar's status <select>, and
// FilterBar's status filter chips).
export const statusLabels: Record<Status, MessageDescriptor> = {
  backlog: msg`Backlog`,
  playing: msg`Playing`,
  beaten: msg`Beaten`,
  completed: msg`Completed`,
  dropped: msg`Dropped`,
  shelved: msg`Shelved`,
}

// statusWireLabels: identity-preserving - CopyDetailsFields' status <select>
// and the dashboard's by-status breakdown have never been prettified,
// so the label text is the raw wire value. An unknown future wire
// value falls back to rendering itself raw at the call site.
export const statusWireLabels: Record<Status, MessageDescriptor> = {
  backlog: msg`backlog`,
  playing: msg`playing`,
  beaten: msg`beaten`,
  completed: msg`completed`,
  dropped: msg`dropped`,
  shelved: msg`shelved`,
}

// conditionLabels: the Title-case form shared by CopyDetailsFields'
// condition selects and FilterBar's condition filter chips.
export const conditionLabels: Record<Condition, MessageDescriptor> = {
  mint: msg`Mint`,
  near_mint: msg`Near mint`,
  very_good: msg`Very good`,
  good: msg`Good`,
  acceptable: msg`Acceptable`,
  poor: msg`Poor`,
}

// packagingWireLabels: identity-preserving, like statusWireLabels -
// EntryTable's packaging column and CopyDetailsFields' packaging <select>
// have never been prettified.
export const packagingWireLabels: Record<Packaging, MessageDescriptor> = {
  sealed: msg`sealed`,
  cib: msg`cib`,
  loose: msg`loose`,
}

// packagingChipLabels: the Title-case form FilterBar's packaging
// filter chips use - a genuinely different casing from
// packagingWireLabels above, not a duplicate of it.
export const packagingChipLabels: Record<Packaging, MessageDescriptor> = {
  sealed: msg`Sealed`,
  cib: msg`CIB`,
  loose: msg`Loose`,
}

// itemTypeWireLabels: identity-preserving, like statusWireLabels - the
// EntryDetail byline and the dashboard's by-item-type breakdown have
// never been prettified.
export const itemTypeWireLabels: Record<ItemType, MessageDescriptor> = {
  game: msg`game`,
  console: msg`console`,
  accessory: msg`accessory`,
}

// productTypeWireLabels: a Product's own type field (PromoteCandidates,
// UnmatchedWorklist, ProductLookup, ConfirmStep) reaches one more value
// than ItemType above - pc_listing, a priced catalog listing rather than
// a collectible item - so it cannot just reuse itemTypeWireLabels.
// game/console/accessory stay identity-preserving like itemTypeWireLabels;
// pc_listing takes the same noun ManualMatchPicker's prose already uses
// for the concept, since the bare wire word is not a real word to show.
export const productTypeWireLabels: Record<Product['type'], MessageDescriptor> = {
  game: msg`game`,
  console: msg`console`,
  accessory: msg`accessory`,
  pc_listing: msg`price listing`,
}

// Visibility covers shelves and saved views alike (SavedView's own
// field), the same three-value wire enum VisibilityControl and
// ShelfManager both render - SavedView['visibility'] stands in for the
// type rather than duplicating that literal union a third time.

// visibilityLabels: the Title-case form VisibilityControl's
// lock/link/globe segmented control uses for its button aria-labels.
export const visibilityLabels: Record<SavedView['visibility'], MessageDescriptor> = {
  private: msg`Private`,
  unlisted: msg`Unlisted`,
  listed: msg`Listed`,
}

// visibilityWireLabels: identity-preserving - ShelfManager's own shelf
// list has never been prettified, so its visibility badge stays the
// raw wire value. A genuinely different casing from visibilityLabels
// above, not a duplicate: VisibilityControl and ShelfManager are two
// different pieces of UI that happen to share the same enum.
export const visibilityWireLabels: Record<SavedView['visibility'], MessageDescriptor> = {
  private: msg`private`,
  unlisted: msg`unlisted`,
  listed: msg`listed`,
}
