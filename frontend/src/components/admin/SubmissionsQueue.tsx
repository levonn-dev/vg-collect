import { Plural, Trans, useLingui } from '@lingui/react/macro'
import { useInfiniteQuery, useQuery, useQueryClient } from '@tanstack/react-query'
import { useState } from 'react'
import { Link } from 'react-router'
import { fetchProfileCards, fetchSubmissions } from '../../api/admin'
import type { AdminSubmission, ProfileCard } from '../../api/admin'
import type { ApiError } from '../../api/client'
import { itemTypeWireLabels } from '../../lib/enumLabels'
import { formatDate } from '../../lib/format'
import { btnSecondaryXs } from '../../lib/formStyles'
import { offsetNextPageParam } from '../../lib/pagination'
import { refetchWarning, renderQueryState } from '../../lib/queryBoundary'
import { regionLabelText } from '../../lib/regionLabels'
import LoadMoreButton from '../LoadMoreButton'
import ReviewPanel, { verdictErrorMessage } from './ReviewPanel'

// Private handle shows as plain text (no public page); a missing card
// (loading/error/absent) falls back to the short id so the cell is never blank.
function SubmitterCell({ card, userId }: { card?: ProfileCard; userId: string }) {
  if (!card) return <span className="font-mono text-xs">{userId.slice(0, 8)}</span>
  if (card.profile_visibility === 'private') return <span>{card.handle}</span>
  return <Link to={`/u/${card.handle}`} className="underline hover:text-gray-600">{card.handle}</Link>
}

// Rows show the entry's CURRENT fields (live); a verdict invalidates admin
// queries so resolved rows leave the list.
export default function SubmissionsQueue() {
  const { t, i18n } = useLingui()
  const queryClient = useQueryClient()
  const [reviewing, setReviewing] = useState<AdminSubmission | null>(null)
  // A raced 409 unmounts the panel before its inline message paints, so the
  // reason shows here after the row leaves. Holds the error, not a rendered
  // message, so a locale switch rephrases it instead of leaving stale text.
  const [notice, setNotice] = useState<ApiError | null>(null)
  const list = useInfiniteQuery({
    queryKey: ['admin', 'submissions'],
    queryFn: ({ pageParam }) => fetchSubmissions(pageParam),
    initialPageParam: 0,
    getNextPageParam: (last, pages) => offsetNextPageParam(last, pages, (p) => p.submissions.length),
  })

  // Hoisted above the early returns below: hooks must run unconditionally, so
  // nothing calling useQuery/useState can sit after one.
  const rows = list.data?.pages.flatMap((p) => p.submissions) ?? []
  const ids = [...new Set(rows.map((s) => s.user_id))]
  const profiles = useQuery({
    queryKey: ['admin', 'profiles', ids],
    queryFn: () => fetchProfileCards(ids),
    enabled: ids.length > 0,
    staleTime: 60_000,
  })
  const cardsById = new Map((profiles.data?.profiles ?? []).map((c) => [c.user_id, c]))

  const done = (error?: ApiError) => {
    setReviewing(null)
    setNotice(error ?? null)
    void queryClient.invalidateQueries({ queryKey: ['admin'] })
  }

  if (list.isPending || (list.isError && list.data === undefined)) {
    return renderQueryState(list, {
      size: 'subsection',
      className: 'mt-4',
      role: 'alert',
      loading: <Trans>Loading queue...</Trans>,
      error: <Trans>The queue could not be loaded.</Trans>,
    })
  }

  const total = list.data.pages[0].total_count

  return (
    <section aria-label={t`Catalog submissions`} className="mt-6">
      <h3 className="text-base font-semibold">
        <Plural value={total} one="# pending submission" other="# pending submissions" />
      </h3>
      {refetchWarning(list)}
      {notice && (
        <p role="status" className="mt-2 rounded bg-amber-50 p-2 text-sm text-amber-800">
          {verdictErrorMessage(notice, i18n)}
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
              <td className="py-1 pr-2">{i18n._(itemTypeWireLabels[s.item_type])}</td>
              <td className="py-1 pr-2">{s.platform_name ?? ''}</td>
              <td className="py-1 pr-2">{[regionLabelText(i18n, s.region), s.edition].filter(Boolean).join(' / ')}</td>
              <td className="py-1 pr-2">
                <SubmitterCell card={cardsById.get(s.user_id)} userId={s.user_id} />
              </td>
              <td className="py-1 pr-2">{formatDate(s.created_at)}</td>
              <td className="py-1">
                <button
                  type="button"
                  onClick={() => {
                    setReviewing(s)
                    setNotice(null)
                  }}
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
      {/* key={reviewing.id}: without it, switching rows reuses the same
          mounted instance, carrying prefilled fields/adopt view along. */}
      {reviewing && <ReviewPanel key={reviewing.id} submission={reviewing} onDone={done} />}
    </section>
  )
}
