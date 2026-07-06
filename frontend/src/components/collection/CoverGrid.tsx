import type { ReactNode } from 'react'
import { Link } from 'react-router'
import type { Entry } from '../../api/collection'
import { formatCents } from '../../lib/format'

interface CoverGridProps {
  entries: Entry[]
  pinSlot?: (e: Entry) => ReactNode
}

export default function CoverGrid({ entries, pinSlot }: CoverGridProps) {
  return (
    <ul className="grid grid-cols-2 gap-4 sm:grid-cols-3 md:grid-cols-4 lg:grid-cols-6">
      {entries.map((e) => (
        <li key={e.id} className="rounded border border-gray-200 p-2">
          <Link to={`/entries/${e.id}`} className="block">
            {e.cover_url ? (
              <img src={e.cover_url} alt="" className="mb-2 aspect-[3/4] w-full rounded object-cover" />
            ) : (
              <div
                aria-hidden="true"
                className="mb-2 flex aspect-[3/4] w-full items-center justify-center rounded bg-gray-200 text-3xl font-bold text-gray-500"
              >
                {e.display_name.charAt(0)}
              </div>
            )}
            <span className="line-clamp-2 text-sm font-medium">{e.display_name}</span>
          </Link>
          <p className="mt-1 flex items-center justify-between text-xs text-gray-500">
            <span>{e.platform?.name ?? '-'}</span>
            <span>{formatCents(e.value_cents) ?? '-'}</span>
          </p>
          <p className="mt-1 text-xs">
            {pinSlot ? pinSlot(e) : e.pinned && <span aria-label="Pinned">{'\u2605'}</span>}
          </p>
        </li>
      ))}
    </ul>
  )
}
