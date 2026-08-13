import { useLingui } from '@lingui/react/macro'
import type { ReactNode } from 'react'
import type { Entry } from '../../api/collection'
import { entryCover, entryTitle, entryTitleLang, titleFormFor } from '../../lib/productTitle'
import { useDisplayMoney } from '../../lib/useDisplayMoney'
import ItemTypeIcon from '../ItemTypeIcon'
import EntryLink from './EntryLink'
import { rowMeta, type EntryRow } from './rowMeta'

interface CoverGridProps {
  entries: EntryRow[]
  pinSlot?: (e: Entry) => ReactNode
  // linkTo lets shared pages retarget or suppress row links; null
  // renders plain text.
  linkTo?: (e: EntryRow) => string | null
  // shared omits the value line - a SharedEntry carries no price data.
  shared?: boolean
}

export default function CoverGrid({ entries, pinSlot, linkTo, shared }: CoverGridProps) {
  // CoverGrid has no translated string of its own, but useLingui()
  // is still required here: rowMeta's pin badge needs a live i18n
  // (see rowMeta.tsx), and this call is what subscribes CoverGrid to
  // locale changes so a mounted grid - e.g. SharedShelf's read-only
  // view, which passes no pinSlot - re-renders that badge instead of
  // going stale after a live locale switch.
  const { i18n } = useLingui()
  const money = useDisplayMoney()
  const form = titleFormFor(i18n.locale)
  return (
    <ul className="grid grid-cols-2 gap-4 sm:grid-cols-3 md:grid-cols-4 lg:grid-cols-6">
      {entries.map((e) => {
        const meta = rowMeta(e, money, i18n, { pinSlot })
        const coverUrl = entryCover(e)
        const cover = (
          <>
            {coverUrl ? (
              <img
                src={coverUrl}
                alt=""
                // Hardware images are platform logos: contain, never crop.
                className={`mb-2 aspect-[3/4] w-full rounded ${
                  e.item_type === 'game' ? 'object-cover' : 'bg-gray-50 object-contain p-2'
                }`}
              />
            ) : (
              <div
                aria-hidden="true"
                className="mb-2 flex aspect-[3/4] w-full items-center justify-center rounded bg-gray-100 text-gray-400"
              >
                <ItemTypeIcon type={e.item_type} className="h-10 w-10" />
              </div>
            )}
            <span className="line-clamp-2 text-sm font-medium">
              <span lang={entryTitleLang(e, form)}>{entryTitle(e, form)}</span>
            </span>
          </>
        )
        return (
          <li key={e.id} className="rounded border border-gray-200 p-2">
            <EntryLink entry={e} linkTo={linkTo} as="div" plainClassName="block" linkClassName="block">
              {cover}
            </EntryLink>
            <p className="mt-1 flex items-center justify-between text-xs text-gray-500">
              <span>{meta.platform}</span>
              {!shared && <span>{meta.value}</span>}
            </p>
            <p className="mt-1 text-xs">
              {meta.pin}
            </p>
          </li>
        )
      })}
    </ul>
  )
}
