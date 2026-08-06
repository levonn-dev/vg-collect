import { useLingui } from '@lingui/react/macro'
import type { ReactNode } from 'react'
import { Link } from 'react-router'
import type { Entry } from '../../api/collection'
import { entryTitle, entryTitleLang, titleFormFor } from '../../lib/productTitle'
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
  const { i18n } = useLingui()
  const money = useDisplayMoney()
  const form = titleFormFor(i18n.locale)
  return (
    <ul className="divide-y divide-gray-100 text-sm">
      {entries.map((e) => {
        const meta = rowMeta(e, money, i18n, { pinSlot })
        return (
          <li key={e.id} className="flex items-center gap-2 py-1">
            {meta.pin}
            {linkTo?.(e) === null
              ? <span className="font-medium"><span lang={entryTitleLang(e, form)}>{entryTitle(e, form)}</span></span>
              : (
                <Link to={(linkTo ?? (x => `/entries/${x.id}`))(e)!} className="font-medium hover:underline">
                  <span lang={entryTitleLang(e, form)}>{entryTitle(e, form)}</span>
                </Link>
              )}
            <span className="text-gray-400">{meta.platform}</span>
            {isFullEntry(e) && <span className="text-gray-400">{i18n._(statusLabels[e.status])}</span>}
            {!shared && <span className="ml-auto text-gray-500">{meta.value}</span>}
          </li>
        )
      })}
    </ul>
  )
}
