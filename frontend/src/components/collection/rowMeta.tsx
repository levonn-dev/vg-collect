import { t } from '@lingui/core/macro'
import type { I18n } from '@lingui/core'
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
//
// i18n is threaded in by the caller rather than read off the
// @lingui/core singleton directly: rowMeta is a plain function, not
// a component, so it cannot call the real useLingui() hook itself,
// and reading the singleton would render the correct string today
// but never trigger a re-render on a later locale switch (only a
// component that itself subscribes via useLingui() re-renders on
// change). Requiring the parameter forces every caller - including
// CoverGrid, which otherwise has no i18n-touching string of its own
// - to obtain it from their own useLingui() call, so the static
// "Pinned" badge stays live even when it is the only translated
// thing on screen.
export function rowMeta(
  entry: EntryRow,
  money: DisplayMoney,
  i18n: I18n,
  opts: { pinSlot?: (e: Entry) => ReactNode; pinTrailingSpace?: boolean } = {},
) {
  const { pinSlot, pinTrailingSpace } = opts
  const full = isFullEntry(entry) ? entry : undefined
  return {
    pin: pinSlot && full
      ? pinSlot(full)
      : entry.pinned && <span aria-label={t(i18n)`Pinned`}>{pinTrailingSpace ? STAR_SPACE : STAR}</span>,
    platform: entry.platform?.name ?? '-',
    value: full ? (money.entryValue(full) ?? '-') : '-',
  }
}
