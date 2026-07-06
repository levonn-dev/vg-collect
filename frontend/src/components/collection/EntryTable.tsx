import type { ReactNode } from 'react'
import { Link } from 'react-router'
import type { Entry } from '../../api/collection'
import { formatCents, releaseYear } from '../../lib/format'

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
  entries: Entry[]
  // pinSlot lets the page swap the static star for an interactive
  // control without this table knowing about mutations.
  pinSlot?: (e: Entry) => ReactNode
}

export default function EntryTable({ entries, pinSlot }: EntryTableProps) {
  return (
    <table className="w-full text-left text-sm">
      <thead>
        <tr className="border-b border-gray-200 text-xs uppercase tracking-wide text-gray-500">
          <th className="py-2 pr-3">Name</th>
          <th className="py-2 pr-3">Platform</th>
          <th className="py-2 pr-3">Status</th>
          <th className="py-2 pr-3">Packaging</th>
          <th className="py-2 pr-3">Rating</th>
          <th className="py-2 pr-3 text-right">Paid</th>
          <th className="py-2 text-right">Value</th>
        </tr>
      </thead>
      <tbody>
        {entries.map((e) => (
          <tr key={e.id} className="border-b border-gray-100">
            <td className="py-2 pr-3">
              {pinSlot ? pinSlot(e) : e.pinned && <span aria-label="Pinned">{'\u2605'} </span>}
              <Link to={`/entries/${e.id}`} className="font-medium hover:underline">
                {e.display_name}
              </Link>
              {e.edition && <span className="ml-2 text-xs text-gray-500">{e.edition}</span>}
              {releaseYear(e.first_release_date) && (
                <span className="ml-2 text-xs text-gray-400">{releaseYear(e.first_release_date)}</span>
              )}
            </td>
            <td className="py-2 pr-3">{e.platform?.name ?? '-'}</td>
            <td className="py-2 pr-3">{statusLabels[e.status]}</td>
            <td className="py-2 pr-3">{e.packaging}</td>
            <td className="py-2 pr-3">{e.rating ?? '-'}</td>
            <td className="py-2 pr-3 text-right">{formatCents(e.price_paid_cents, e.currency) ?? '-'}</td>
            <td className="py-2 text-right">{formatCents(e.value_cents) ?? '-'}</td>
          </tr>
        ))}
      </tbody>
    </table>
  )
}
