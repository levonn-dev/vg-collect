import { Plural, Trans, useLingui } from '@lingui/react/macro'
import { useInfiniteQuery, useQueryClient } from '@tanstack/react-query'
import { useState } from 'react'
import { fetchPromoteCandidates } from '../../api/admin'
import { formatDate } from '../../lib/format'
import { btnSecondaryXs } from '../../lib/formStyles'
import { offsetNextPageParam } from '../../lib/pagination'
import { renderQueryState } from '../../lib/queryBoundary'
import LoadMoreButton from '../LoadMoreButton'
import PromotePanel from './PromotePanel'

// PromoteCandidates pages through every community product the
// nightly sweep flagged with a plausible provider match. found_at
// marks the sweep's LAST CONFIRMATION of a candidate, not its first
// discovery - the sweep re-confirms every candidate nightly and
// rewrites the whole array, so the column reads "Last confirmed",
// never "found". Promoting or dismissing changes the list itself, so a
// successful review invalidates admin queries and loaded pages
// refetch with their stored pageParam (rows that got promoted, or
// whose last candidate got dismissed, leave the list).
export default function PromoteCandidates() {
  const { t } = useLingui()
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

  if (list.isPending || list.isError) {
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
  // Derive the open row from live data so a dismiss (which invalidates
  // and refetches) refreshes the panel's candidates in place; when the
  // product leaves the list (promoted, or its last candidate dismissed)
  // the panel closes on its own.
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
              <td className="py-1 pr-2">{row.product.type}</td>
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
      {reviewing && <PromotePanel product={reviewing.product} candidates={reviewing.candidates} onDone={done} />}
    </section>
  )
}
