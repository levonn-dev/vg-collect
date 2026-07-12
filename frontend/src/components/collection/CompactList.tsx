import type { ReactNode } from 'react'
import { Link } from 'react-router'
import type { Entry } from '../../api/collection'
import { useDisplayMoney } from '../../lib/useDisplayMoney'
import { statusLabels } from './EntryTable'

interface CompactListProps {
  entries: Entry[]
  pinSlot?: (e: Entry) => ReactNode
}

export default function CompactList({ entries, pinSlot }: CompactListProps) {
  const money = useDisplayMoney()
  return (
    <ul className="divide-y divide-gray-100 text-sm">
      {entries.map((e) => (
        <li key={e.id} className="flex items-center gap-2 py-1">
          {pinSlot ? pinSlot(e) : e.pinned && <span aria-label="Pinned">{'\u2605'}</span>}
          <Link to={`/entries/${e.id}`} className="font-medium hover:underline">
            {e.display_name}
          </Link>
          <span className="text-gray-400">{e.platform?.name ?? '-'}</span>
          <span className="text-gray-400">{statusLabels[e.status]}</span>
          <span className="ml-auto text-gray-500">{money.entryValue(e) ?? '-'}</span>
        </li>
      ))}
    </ul>
  )
}
