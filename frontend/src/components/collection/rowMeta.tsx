import type { ReactNode } from 'react'
import type { Entry } from '../../api/collection'
import type { components } from '../../api/schema'
import type { DisplayMoney } from '../../lib/useDisplayMoney'

const STAR = '\u2605'
const STAR_SPACE = '\u2605 '

// SharedEntry is the cross-user whitelist projection (money and
// personal fields never appear); EntryRow is the union EntryTable,
// CoverGrid, and CompactList actually accept, so the same three views
// render either the caller's own Entry rows (Collection) or another
// user's read-only SharedEntry rows (SharedShelf).
export type SharedEntry = components['schemas']['SharedEntry']
export type EntryRow = Entry | SharedEntry

// isFullEntry narrows EntryRow to Entry. status is required on Entry
// and entirely absent from SharedEntry - the cleanest single-field
// discriminant between "my own row" and "someone else's shared row".
export function isFullEntry(e: EntryRow): e is Entry {
  return 'status' in e
}

// rowMeta computes the pin badge, platform label, and display value
// that EntryTable, CoverGrid, and CompactList each place into their
// own row layout; only EntryTable's plain-text flow (no flex gap)
// needs the pin badge's own trailing space to separate it from the
// name link that follows it. pinSlot keeps its Entry-only signature
// (a shared SharedEntry listing never passes one) and simply never
// fires for a SharedEntry row; the money value falls back to '-' the
// same way - a SharedEntry carries no price data to show.
export function rowMeta(
  entry: EntryRow,
  money: DisplayMoney,
  opts: { pinSlot?: (e: Entry) => ReactNode; pinTrailingSpace?: boolean } = {},
) {
  const { pinSlot, pinTrailingSpace } = opts
  const full = isFullEntry(entry) ? entry : undefined
  return {
    pin: pinSlot && full
      ? pinSlot(full)
      : entry.pinned && <span aria-label="Pinned">{pinTrailingSpace ? STAR_SPACE : STAR}</span>,
    platform: entry.platform?.name ?? '-',
    value: full ? (money.entryValue(full) ?? '-') : '-',
  }
}
