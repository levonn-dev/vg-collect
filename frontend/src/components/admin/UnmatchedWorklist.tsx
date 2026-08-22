import { Trans, useLingui } from '@lingui/react/macro'
import { useInfiniteQuery, useQueryClient } from '@tanstack/react-query'
import { useState } from 'react'
import { fetchUnmatchedProducts } from '../../api/admin'
import type { Product } from '../../api/catalog'
import { formatDate } from '../../lib/format'
import { btnSecondaryXs } from '../../lib/formStyles'
import { offsetNextPageParam } from '../../lib/pagination'
import { renderQueryState } from '../../lib/queryBoundary'
import LoadMoreButton from '../LoadMoreButton'
import MappingFix from './MappingFix'

// UnmatchedWorklist pages through every product with no mapping
// (held ones flagged). Fixing a row changes the list itself, so a
// successful fix invalidates admin queries and loaded pages refetch
// with their stored pageParam (rows that got fixed leave the list).
export default function UnmatchedWorklist() {
  const { t } = useLingui()
  const queryClient = useQueryClient()
  const [fixing, setFixing] = useState<Product | null>(null)
  const list = useInfiniteQuery({
    queryKey: ['admin', 'unmatched'],
    queryFn: ({ pageParam }) => fetchUnmatchedProducts(pageParam),
    initialPageParam: 0,
    getNextPageParam: (last, pages) => offsetNextPageParam(last, pages, (p) => p.products.length),
  })

  const done = () => {
    setFixing(null)
    void queryClient.invalidateQueries({ queryKey: ['admin'] })
  }

  if (list.isPending || list.isError) {
    return renderQueryState(list, {
      size: 'subsection',
      className: 'mt-4',
      role: 'alert',
      loading: <Trans>Loading worklist...</Trans>,
      error: <Trans>The worklist could not be loaded.</Trans>,
    })
  }

  const products = list.data.pages.flatMap((p) => p.products)
  const total = list.data.pages[0].total_count

  return (
    <section aria-label={t`Unmatched products`} className="mt-6">
      <h3 className="text-base font-semibold"><Trans>{total} unmatched products</Trans></h3>
      <table className="mt-2 w-full text-left text-sm">
        <thead>
          <tr className="border-b border-gray-200 text-gray-500">
            <th className="py-1 pr-2 font-normal"><Trans>Name</Trans></th>
            <th className="py-1 pr-2 font-normal"><Trans>Type</Trans></th>
            <th className="py-1 pr-2 font-normal"><Trans>Platform / console</Trans></th>
            <th className="py-1 pr-2 font-normal"><Trans>Region / edition / variant</Trans></th>
            <th className="py-1 pr-2 font-normal"><Trans>Updated</Trans></th>
            <th className="py-1 font-normal"></th>
          </tr>
        </thead>
        <tbody>
          {products.map((p) => (
            <tr key={p.id} className="border-b border-gray-100">
              <td className="py-1 pr-2">
                {p.name}
                {p.match_hold && (
                  <span className="ml-2 rounded bg-amber-50 px-1.5 py-0.5 text-xs font-semibold text-amber-800">
                    <Trans>held</Trans>
                  </span>
                )}
              </td>
              <td className="py-1 pr-2">{p.type}</td>
              <td className="py-1 pr-2">{p.platform?.name ?? ''}</td>
              <td className="py-1 pr-2">{[p.region, p.edition, p.variant].filter(Boolean).join(' / ')}</td>
              <td className="py-1 pr-2">{formatDate(p.updated_at)}</td>
              <td className="py-1">
                <button
                  type="button"
                  onClick={() => setFixing(p)}
                  className={btnSecondaryXs}
                >
                  <Trans>Fix mapping</Trans>
                </button>
              </td>
            </tr>
          ))}
        </tbody>
      </table>
      <LoadMoreButton query={list} className="mt-2" />
      {fixing && <MappingFix product={fixing} onDone={done} />}
    </section>
  )
}
