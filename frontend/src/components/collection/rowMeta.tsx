import type { ReactNode } from 'react'
import type { Entry } from '../../api/collection'
import type { DisplayMoney } from '../../lib/useDisplayMoney'

const STAR = '\u2605'
const STAR_SPACE = '\u2605 '

// rowMeta computes the pin badge, platform label, and display value
// that EntryTable, CoverGrid, and CompactList each place into their
// own row layout; only EntryTable's plain-text flow (no flex gap)
// needs the pin badge's own trailing space to separate it from the
// name link that follows it.
export function rowMeta(
  entry: Entry,
  money: DisplayMoney,
  opts: { pinSlot?: (e: Entry) => ReactNode; pinTrailingSpace?: boolean } = {},
) {
  const { pinSlot, pinTrailingSpace } = opts
  return {
    pin: pinSlot
      ? pinSlot(entry)
      : entry.pinned && <span aria-label="Pinned">{pinTrailingSpace ? STAR_SPACE : STAR}</span>,
    platform: entry.platform?.name ?? '-',
    value: money.entryValue(entry) ?? '-',
  }
}
