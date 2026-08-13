import { Plural, Trans, useLingui } from '@lingui/react/macro'
import { useInfiniteQuery, useQuery, useQueryClient } from '@tanstack/react-query'
import { useState } from 'react'
import { Link } from 'react-router'
import { fetchProfileCards, fetchSubmissions } from '../../api/admin'
import type { AdminSubmission, ProfileCard } from '../../api/admin'
import type { ApiError } from '../../api/client'
import { offsetNextPageParam } from '../../lib/pagination'
import { renderQueryState } from '../../lib/queryBoundary'
import { regionLabelText } from '../../lib/regionLabels'
import LoadMoreButton from '../LoadMoreButton'
import ReviewPanel, { verdictErrorMessage } from './ReviewPanel'

// SubmitterCell resolves a row's user_id against the batched profile
// cards: a listed or unlisted handle links to the public profile, a
// private handle shows as plain text since there is no page to send an
// admin to, and a missing card - the batch still loading, erroring, or
// the id simply absent from the response - falls back to the short id
// so the cell is never blank.
function SubmitterCell({ card, userId }: { card?: ProfileCard; userId: string }) {
  if (!card) return <span className="font-mono text-xs">{userId.slice(0, 8)}</span>
  if (card.profile_visibility === 'private') return <span>{card.handle}</span>
  return <Link to={`/u/${card.handle}`} className="underline hover:text-gray-600">{card.handle}</Link>
}

// SubmissionsQueue pages the pending catalog submissions oldest
// first. Proposals are live (the row shows the entry's CURRENT
// fields); a verdict invalidates the admin queries so resolved rows
// leave the list.
export default function SubmissionsQueue() {
  const { t, i18n } = useLingui()
  const queryClient = useQueryClient()
  const [reviewing, setReviewing] = useState<AdminSubmission | null>(null)
  // A transient notice the panel carries up on close: a raced 409
  // unmounts the panel before its inline message paints, so the reason
  // is shown here, at the queue, after the row leaves. The error is
  // what is held, not its message, so a locale switch while the notice
  // is up rephrases it instead of leaving stale text on screen.
  const [notice, setNotice] = useState<ApiError | null>(null)
  const list = useInfiniteQuery({
    queryKey: ['admin', 'submissions'],
    queryFn: ({ pageParam }) => fetchSubmissions(pageParam),
    initialPageParam: 0,
    getNextPageParam: (last, pages) => offsetNextPageParam(last, pages, (p) => p.submissions.length),
  })

  // Hoisted above the isPending/isError returns below, alongside the
  // profile-cards query: hooks must run unconditionally on every
  // render, so nothing that calls useQuery/useState can sit after an
  // early return.
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

  if (list.isPending || list.isError) {
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
              <td className="py-1 pr-2">{s.item_type}</td>
              <td className="py-1 pr-2">{s.platform_name ?? ''}</td>
              <td className="py-1 pr-2">{[regionLabelText(i18n, s.region), s.edition].filter(Boolean).join(' / ')}</td>
              <td className="py-1 pr-2">
                <SubmitterCell card={cardsById.get(s.user_id)} userId={s.user_id} />
              </td>
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
      <LoadMoreButton query={list} className="mt-2" />
      {/* key={reviewing.id}: without it, switching the reviewed row while
          the panel is open reuses the same mounted instance, so its
          prefilled fields and adopt view carry over from the old row
          instead of resetting for the new one. */}
      {reviewing && <ReviewPanel key={reviewing.id} submission={reviewing} onDone={done} />}
    </section>
  )
}
