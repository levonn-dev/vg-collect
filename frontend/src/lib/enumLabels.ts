import { msg } from '@lingui/core/macro'
import type { MessageDescriptor } from '@lingui/core'
import type { Product } from '../api/catalog'
import type { SavedView } from '../api/collection'
import type { Condition, ItemType, Packaging, Status } from './listParams'

// Typed as Record<Enum, MessageDescriptor>: a new enum member fails to
// compile here, not renders blank.

// Title-case form (EntryTable, CompactList, BulkEditBar, FilterBar status UI).
export const statusLabels: Record<Status, MessageDescriptor> = {
  backlog: msg`Backlog`,
  playing: msg`Playing`,
  beaten: msg`Beaten`,
  completed: msg`Completed`,
  dropped: msg`Dropped`,
  shelved: msg`Shelved`,
}

// Identity-preserving: CopyDetailsFields select and dashboard
// breakdown show the raw wire value, never prettified.
export const statusWireLabels: Record<Status, MessageDescriptor> = {
  backlog: msg`backlog`,
  playing: msg`playing`,
  beaten: msg`beaten`,
  completed: msg`completed`,
  dropped: msg`dropped`,
  shelved: msg`shelved`,
}

// Title-case form shared by CopyDetailsFields' condition selects and
// FilterBar's chips.
export const conditionLabels: Record<Condition, MessageDescriptor> = {
  mint: msg`Mint`,
  near_mint: msg`Near mint`,
  very_good: msg`Very good`,
  good: msg`Good`,
  acceptable: msg`Acceptable`,
  poor: msg`Poor`,
}

// Identity-preserving like statusWireLabels: EntryTable column and
// CopyDetailsFields select, never prettified.
export const packagingWireLabels: Record<Packaging, MessageDescriptor> = {
  sealed: msg`sealed`,
  cib: msg`cib`,
  loose: msg`loose`,
}

// Title-case form for FilterBar's chips; a different casing from
// packagingWireLabels, not a duplicate.
export const packagingChipLabels: Record<Packaging, MessageDescriptor> = {
  sealed: msg`Sealed`,
  cib: msg`CIB`,
  loose: msg`Loose`,
}

// Identity-preserving like statusWireLabels: EntryDetail byline and
// dashboard breakdown, never prettified.
export const itemTypeWireLabels: Record<ItemType, MessageDescriptor> = {
  game: msg`game`,
  console: msg`console`,
  accessory: msg`accessory`,
}

// Product's type field adds pc_listing over ItemType (a priced listing,
// not a collectible), so it can't reuse itemTypeWireLabels; pc_listing
// borrows ManualMatchPicker's noun since the bare wire word isn't real.
export const productTypeWireLabels: Record<Product['type'], MessageDescriptor> = {
  game: msg`game`,
  console: msg`console`,
  accessory: msg`accessory`,
  pc_listing: msg`price listing`,
}

// Covers shelves and saved views (SavedView['visibility']) rather than
// duplicating the literal union.

// Title-case form for VisibilityControl's lock/link/globe control aria-labels.
export const visibilityLabels: Record<SavedView['visibility'], MessageDescriptor> = {
  private: msg`Private`,
  unlisted: msg`Unlisted`,
  listed: msg`Listed`,
}

// Identity-preserving for ShelfManager's badge, never prettified; a
// different casing from visibilityLabels, not a duplicate.
export const visibilityWireLabels: Record<SavedView['visibility'], MessageDescriptor> = {
  private: msg`private`,
  unlisted: msg`unlisted`,
  listed: msg`listed`,
}
