import type { ReactNode } from 'react'
import { Link } from 'react-router'
import type { Entry } from '../../api/collection'
import { useDisplayMoney } from '../../lib/useDisplayMoney'
import { statusLabels } from './EntryTable'
import { isFullEntry, rowMeta, type EntryRow } from './rowMeta'

interface CompactListProps {
  entries: EntryRow[]
  pinSlot?: (e: Entry) => ReactNode
  // linkTo lets shared pages retarget or suppress row links; null
  // renders plain text.
  linkTo?: (e: EntryRow) => string | null
  // shared omits the trailing value span - the status span is already
  // isFullEntry-gated, so a SharedEntry row never shows one anyway.
  shared?: boolean
}

export default function CompactList({ entries, pinSlot, linkTo, shared }: CompactListProps) {
  const money = useDisplayMoney()
  return (
    <ul className="divide-y divide-gray-100 text-sm">
      {entries.map((e) => {
        const meta = rowMeta(e, money, { pinSlot })
        return (
          <li key={e.id} className="flex items-center gap-2 py-1">
            {meta.pin}
            {linkTo?.(e) === null
              ? <span className="font-medium">{e.display_name}</span>
              : <Link to={(linkTo ?? (x => `/entries/${x.id}`))(e)!} className="font-medium hover:underline">{e.display_name}</Link>}
            <span className="text-gray-400">{meta.platform}</span>
            {isFullEntry(e) && <span className="text-gray-400">{statusLabels[e.status]}</span>}
            {!shared && <span className="ml-auto text-gray-500">{meta.value}</span>}
          </li>
        )
      })}
    </ul>
  )
}
