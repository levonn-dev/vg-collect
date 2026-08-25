import { Plural, Trans, useLingui } from '@lingui/react/macro'
import { useInfiniteQuery, useQueryClient } from '@tanstack/react-query'
import { useState } from 'react'
import { fetchPromoteCandidates } from '../../api/admin'
import { formatDate } from '../../lib/format'
import { productTypeWireLabels } from '../../lib/enumLabels'
import { btnSecondaryXs } from '../../lib/formStyles'
import { offsetNextPageParam } from '../../lib/pagination'
import { refetchWarning, renderQueryState } from '../../lib/queryBoundary'
import LoadMoreButton from '../LoadMoreButton'
import PromotePanel from './PromotePanel'

// found_at marks the sweep's LAST CONFIRMATION, not first discovery (the
// sweep rewrites the whole array nightly); the column reads "Last confirmed".
// Promoting/dismissing invalidates admin queries; loaded pages refetch with
// their stored pageParam.
export default function PromoteCandidates() {
  const { t, i18n } = useLingui()
  const queryClient = useQueryClient()
  const [reviewingId, setReviewingId] = useState<string | null>(null)
  const list = useInfiniteQuery({
    queryKey: ['admin', 'candidates'],
    queryFn: ({ pageParam }) => fetchPromoteCandidates(pageParam),
    initialPageParam: 0,
    getNextPageParam: (last, pages) => offsetNextPageParam(last, pages, (p) => p.products.length),
  })

  const done = () => {
    setReviewingId(null)
    void queryClient.invalidateQueries({ queryKey: ['admin'] })
  }

  if (list.isPending || (list.isError && list.data === undefined)) {
    return renderQueryState(list, {
      size: 'subsection',
      className: 'mt-4',
      role: 'alert',
      loading: <Trans>Loading candidates...</Trans>,
      error: <Trans>The promote candidates could not be loaded.</Trans>,
    })
  }

  const rows = list.data.pages.flatMap((p) => p.products)
  const total = list.data.pages[0].total_count
  // Derived from live data so a dismiss refreshes the panel's candidates in
  // place; the panel closes on its own once the product leaves the list.
  const reviewing = reviewingId === null ? null : (rows.find((r) => r.product.id === reviewingId) ?? null)

  return (
    <section aria-label={t`Promote candidates`} className="mt-6">
      <h3 className="text-base font-semibold">
        <Plural
          value={total}
          one="# community product with possible provider matches"
          other="# community products with possible provider matches"
        />
      </h3>
      {refetchWarning(list)}
      <table className="mt-2 w-full text-left text-sm">
        <thead>
          <tr className="border-b border-gray-200 text-gray-500">
            <th className="py-1 pr-2 font-normal"><Trans>Name</Trans></th>
            <th className="py-1 pr-2 font-normal"><Trans>Type</Trans></th>
            <th className="py-1 pr-2 font-normal"><Trans>Top candidate</Trans></th>
            <th className="py-1 pr-2 font-normal"><Trans>Last confirmed</Trans></th>
            <th className="py-1 font-normal"></th>
          </tr>
        </thead>
        <tbody>
          {rows.map((row) => (
            <tr key={row.product.id} className="border-b border-gray-100">
              <td className="py-1 pr-2">{row.product.name}</td>
              <td className="py-1 pr-2">{i18n._(productTypeWireLabels[row.product.type])}</td>
              <td className="py-1 pr-2">
                {row.candidates[0] ? `${row.candidates[0].name} (${row.candidates[0].score.toFixed(2)})` : ''}
              </td>
              <td className="py-1 pr-2">{row.candidates[0] ? formatDate(row.candidates[0].found_at) : ''}</td>
              <td className="py-1">
                <button
                  type="button"
                  onClick={() => setReviewingId(row.product.id)}
                  className={btnSecondaryXs}
                >
                  <Trans>Review</Trans>
                </button>
              </td>
            </tr>
          ))}
        </tbody>
      </table>
      <LoadMoreButton query={list} className="mt-2" />
      {/* key={reviewing.product.id}: without it, switching rows reconciles the
          same PromotePanel in place, leaking attached-listing/picking state. */}
      {reviewing && <PromotePanel key={reviewing.product.id} product={reviewing.product} candidates={reviewing.candidates} onDone={done} />}
    </section>
  )
}
