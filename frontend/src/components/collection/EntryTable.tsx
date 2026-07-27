import type { ReactNode } from 'react'
import { Link } from 'react-router'
import type { Entry } from '../../api/collection'
import { formatCents, releaseYear } from '../../lib/format'
import { useDisplayMoney } from '../../lib/useDisplayMoney'
import { isFullEntry, rowMeta, type EntryRow } from './rowMeta'

// eslint-disable-next-line react-refresh/only-export-components -- shared label map, consumed by other collection views alongside this component.
export const statusLabels: Record<Entry['status'], string> = {
  backlog: 'Backlog',
  playing: 'Playing',
  beaten: 'Beaten',
  completed: 'Completed',
  dropped: 'Dropped',
  shelved: 'Shelved',
}

interface EntryTableProps {
  entries: EntryRow[]
  // pinSlot lets the page swap the static star for an interactive
  // control without this table knowing about mutations.
  pinSlot?: (e: Entry) => ReactNode
  // linkTo lets shared pages retarget or suppress row links; null
  // renders plain text.
  linkTo?: (e: EntryRow) => string | null
  // Adds a leading rank column, 1-based over this render's own
  // entries array - the shared-shelf backlog-rank view's only use.
  numbered?: boolean
  // shared hides the owner-only columns (Status, Rating, Paid, Value)
  // entirely - SharedEntry never carries those fields, so the
  // alternative (rendering every row's cell as a bare '-') is just
  // dead columns on a page the viewer does not own.
  shared?: boolean
  // selectable adds the leading checkbox column (header select-all
  // plus one checkbox per row) for Collection's bulk-edit mode. It
  // defaults off and is never combined with shared - a shared page
  // never offers selection over rows the viewer does not own.
  selectable?: boolean
  selected?: ReadonlySet<string>
  onToggleSelect?: (id: string) => void
}

export default function EntryTable({
  entries, pinSlot, linkTo, numbered, shared, selectable, selected, onToggleSelect,
}: EntryTableProps) {
  const money = useDisplayMoney()
  // Only meaningful while selectable (nothing reads them otherwise),
  // so these skip re-deriving that from selectable itself.
  const selectedCount = entries.filter((e) => selected?.has(e.id)).length
  const allSelected = entries.length > 0 && selectedCount === entries.length
  const someSelected = selectedCount > 0 && !allSelected
  // Bulk-toggles every row of THIS table's own entries through the same
  // per-id callback the row checkboxes use, so the shared selection Set
  // only ever grows or shrinks by ids this table actually owns - a
  // sibling group's table (or a later page) is untouched either way.
  const toggleAll = () => {
    if (!onToggleSelect) return
    for (const e of entries) {
      if (allSelected || selected?.has(e.id) !== true) onToggleSelect(e.id)
    }
  }
  return (
    <table className="w-full text-left text-sm">
      <thead>
        <tr className="border-b border-gray-200 text-xs uppercase tracking-wide text-gray-500">
          {selectable && (
            <th className="py-2 pr-3">
              <input
                type="checkbox"
                aria-label="Select all"
                checked={allSelected}
                ref={(el) => {
                  if (el) el.indeterminate = someSelected
                }}
                onChange={toggleAll}
              />
            </th>
          )}
          {numbered && <th className="py-2 pr-3 text-right">#</th>}
          <th className="py-2 pr-3">Name</th>
          <th className="py-2 pr-3">Platform</th>
          {!shared && <th className="py-2 pr-3">Status</th>}
          <th className="py-2 pr-3">Packaging</th>
          {!shared && <th className="py-2 pr-3">Rating</th>}
          {!shared && <th className="py-2 pr-3 text-right">Paid</th>}
          {!shared && <th className="py-2 text-right">Value ({money.currency})</th>}
        </tr>
      </thead>
      <tbody>
        {entries.map((e, i) => {
          const meta = rowMeta(e, money, { pinSlot, pinTrailingSpace: true })
          return (
            <tr key={e.id} className="border-b border-gray-100">
              {selectable && (
                <td className="py-2 pr-3">
                  <input
                    type="checkbox"
                    aria-label={`Select ${e.display_name}`}
                    checked={selected?.has(e.id) ?? false}
                    onChange={() => onToggleSelect?.(e.id)}
                  />
                </td>
              )}
              {numbered && <td className="py-2 pr-3 text-right text-gray-400">{i + 1}</td>}
              <td className="py-2 pr-3">
                {meta.pin}
                {linkTo?.(e) === null
                  ? <span className="font-medium">{e.display_name}</span>
                  : <Link to={(linkTo ?? (x => `/entries/${x.id}`))(e)!} className="font-medium hover:underline">{e.display_name}</Link>}
                {e.edition && <span className="ml-2 text-xs text-gray-500">{e.edition}</span>}
                {releaseYear(e.first_release_date) && (
                  <span className="ml-2 text-xs text-gray-400">{releaseYear(e.first_release_date)}</span>
                )}
              </td>
              <td className="py-2 pr-3">{meta.platform}</td>
              {!shared && <td className="py-2 pr-3">{isFullEntry(e) ? statusLabels[e.status] : '-'}</td>}
              <td className="py-2 pr-3">{e.packaging}</td>
              {!shared && <td className="py-2 pr-3">{isFullEntry(e) ? (e.rating ?? '-') : '-'}</td>}
              {!shared && (
                <td className="py-2 pr-3 text-right">
                  {isFullEntry(e) ? (formatCents(e.price_paid_cents, e.currency) ?? '-') : '-'}
                </td>
              )}
              {!shared && <td className="py-2 text-right">{meta.value}</td>}
            </tr>
          )
        })}
      </tbody>
    </table>
  )
}
