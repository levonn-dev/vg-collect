import { Plural, Trans, useLingui } from '@lingui/react/macro'
import { useInfiniteQuery, useQueryClient } from '@tanstack/react-query'
import { useState } from 'react'
import { fetchSubmissions } from '../../api/admin'
import type { AdminSubmission } from '../../api/admin'
import ReviewPanel from './ReviewPanel'

// SubmissionsQueue pages the pending catalog submissions oldest
// first. Proposals are live (the row shows the entry's CURRENT
// fields); a verdict invalidates the admin queries so resolved rows
// leave the list.
export default function SubmissionsQueue() {
  const { t } = useLingui()
  const queryClient = useQueryClient()
  const [reviewing, setReviewing] = useState<AdminSubmission | null>(null)
  // A transient notice the panel carries up on close: a raced 409
  // unmounts the panel before its inline message paints, so the reason
  // is shown here, at the queue, after the row leaves.
  const [notice, setNotice] = useState<string | null>(null)
  const list = useInfiniteQuery({
    queryKey: ['admin', 'submissions'],
    queryFn: ({ pageParam }) => fetchSubmissions(pageParam),
    initialPageParam: 0,
    getNextPageParam: (last, pages) => {
      const loaded = pages.reduce((n, p) => n + p.submissions.length, 0)
      return loaded < last.total_count ? loaded : undefined
    },
  })

  const done = (message?: string) => {
    setReviewing(null)
    setNotice(message ?? null)
    void queryClient.invalidateQueries({ queryKey: ['admin'] })
  }

  if (list.isPending) return <p className="mt-4 text-sm text-gray-500"><Trans>Loading queue...</Trans></p>
  if (list.isError)
    return (
      <p role="alert" className="mt-4 text-sm text-red-700">
        <Trans>The queue could not be loaded.</Trans>
      </p>
    )

  const rows = list.data.pages.flatMap((p) => p.submissions)
  const total = list.data.pages[0].total_count

  return (
    <section aria-label={t`Catalog submissions`} className="mt-6">
      <h3 className="text-base font-semibold">
        <Plural value={total} one="# pending submission" other="# pending submissions" />
      </h3>
      {notice && (
        <p role="status" className="mt-2 rounded bg-amber-50 p-2 text-sm text-amber-800">
          {notice}
        </p>
      )}
      <table className="mt-2 w-full text-left text-sm">
        <thead>
          <tr className="border-b border-gray-200 text-gray-500">
            <th className="py-1 pr-2 font-normal"><Trans>Proposed name</Trans></th>
            <th className="py-1 pr-2 font-normal"><Trans>Type</Trans></th>
            <th className="py-1 pr-2 font-normal"><Trans>Platform</Trans></th>
            <th className="py-1 pr-2 font-normal"><Trans>Region / edition</Trans></th>
            <th className="py-1 pr-2 font-normal"><Trans>Submitter</Trans></th>
            <th className="py-1 pr-2 font-normal"><Trans>Submitted</Trans></th>
            <th className="py-1 font-normal"></th>
          </tr>
        </thead>
        <tbody>
          {rows.map((s) => (
            <tr key={s.id} className="border-b border-gray-100">
              <td className="py-1 pr-2">{s.display_name}</td>
              <td className="py-1 pr-2">{s.item_type}</td>
              <td className="py-1 pr-2">{s.platform_name ?? ''}</td>
              <td className="py-1 pr-2">{[s.region, s.edition].filter(Boolean).join(' / ')}</td>
              <td className="py-1 pr-2 font-mono text-xs">{s.user_id.slice(0, 8)}</td>
              <td className="py-1 pr-2">{s.created_at.slice(0, 10)}</td>
              <td className="py-1">
                <button
                  type="button"
                  onClick={() => {
                    setReviewing(s)
                    setNotice(null)
                  }}
                  className="rounded border border-gray-300 px-2 py-0.5 text-xs hover:bg-gray-50"
                >
                  <Trans>Review</Trans>
                </button>
              </td>
            </tr>
          ))}
        </tbody>
      </table>
      {list.hasNextPage && (
        <button
          type="button"
          onClick={() => void list.fetchNextPage()}
          disabled={list.isFetchingNextPage}
          className="mt-2 rounded border border-gray-300 px-3 py-1 text-sm hover:bg-gray-50 disabled:opacity-50"
        >
          <Trans>Load more</Trans>
        </button>
      )}
      {reviewing && <ReviewPanel submission={reviewing} onDone={done} />}
    </section>
  )
}
