import { t } from '@lingui/core/macro'
import type { I18n } from '@lingui/core'
import type { ReactNode } from 'react'
import type { Entry } from '../../api/collection'
import type { components } from '../../api/schema'
import type { DisplayMoney } from '../../lib/useDisplayMoney'

const STAR = '\u2605'
const STAR_SPACE = '\u2605 '

// SharedEntry is the cross-user whitelist projection (money/personal fields
// never appear); EntryRow lets the same three views render either kind.
export type SharedEntry = components['schemas']['SharedEntry']
export type EntryRow = Entry | SharedEntry

// status is required on Entry, absent from SharedEntry: the cleanest
// single-field discriminant between own row and shared row.
export function isFullEntry(e: EntryRow): e is Entry {
  return 'status' in e
}

// Only EntryTable's plain-text flow (no flex gap) needs the pin badge's
// trailing space; pinSlot and the money value both fall back for SharedEntry.
// i18n is threaded by the caller, not read off the @lingui/core singleton:
// rowMeta is a plain function, so it can't call useLingui() itself, and the
// singleton wouldn't re-render on a later locale switch.
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
