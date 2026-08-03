import { Trans, useLingui } from '@lingui/react/macro'
import { t } from '@lingui/core/macro'
import type { I18n } from '@lingui/core'
import { useMutation, useQuery } from '@tanstack/react-query'
import { useState } from 'react'
import { submitVerdict } from '../../api/admin'
import type { AdminSubmission, VerdictRequest } from '../../api/admin'
import type { SearchKind, SearchResult } from '../../api/catalog'
import { resolveProduct, searchCatalog } from '../../api/catalog'
import { ApiError } from '../../api/client'
import { resolveRequestFor } from '../../lib/catalog'
import { releaseYear } from '../../lib/format'
import PlatformPicker from '../catalog/PlatformPicker'
import type { PlatformValue } from '../catalog/PlatformPicker'
import SearchPicker from '../catalog/SearchPicker'
import type { CatalogPick } from '../catalog/SearchPicker'

interface ReviewPanelProps {
  submission: AdminSubmission
  // onDone closes the panel; an optional error rides through to the
  // queue notice, so a raced-verdict explanation is seen after the panel
  // (and its inline message) unmounts. The error travels, not its
  // message: the queue phrases it at render time, so the notice follows
  // a locale switch instead of freezing the language it was raised in.
  onDone: (error?: ApiError) => void
}

// t(i18n) throughout this file, component included: verdictErrorMessage
// is a plain function (cannot call useLingui() itself), so it takes the
// caller's i18n explicitly; the ReviewPanel component uses the same
// explicit form for its own strings rather than importing a second,
// same-named t. PotentialDuplicates below has no such helper to collide
// with, so it uses the plain useLingui()-bound Trans/t as usual.
// eslint-disable-next-line react-refresh/only-export-components -- shared with SubmissionsQueue, which phrases the error this panel hands it, alongside this component.
export function verdictErrorMessage(e: unknown, i18n: I18n): string {
  if (e instanceof ApiError) {
    if (e.code === 'submission_resolved') return t(i18n)`Another admin already resolved this submission.`
    if (e.code === 'unknown_product') return t(i18n)`No product with that id.`
    if (e.code === 'enrichment_unavailable') return t(i18n)`The catalog cannot be reached - try again.`
    if (e.message) return e.message
  }
  return t(i18n)`The verdict failed.`
}

function duplicatesKind(itemType: AdminSubmission['item_type']): SearchKind {
  return itemType === 'game' ? 'game' : 'hardware'
}

// isExactDuplicate flags a duplicate row that is (almost certainly) the
// submitted item itself: identical name (case/whitespace-insensitive),
// and - when both sides carry a platform - identical platform too. A
// row or submission missing a platform name falls back to name-only
// equality, since a data gap is not a mismatch signal. A named helper
// (not inlined JSX) so the badge's rendering behavior is what the test
// targets, not an ad hoc expression.
function isExactDuplicate(submission: AdminSubmission, row: SearchResult): boolean {
  const same = (a: string, b: string) => a.trim().toLowerCase() === b.trim().toLowerCase()
  if (!same(row.name, submission.display_name)) return false
  const rowPlatform = row.console_name ?? row.platform_name
  const subPlatform = submission.platform_name
  if (!rowPlatform || !subPlatform) return true
  return same(rowPlatform, subPlatform)
}

// PotentialDuplicates surfaces the top catalog matches for the
// submitted proposal's own name, so the admin can spot an existing
// provider or community product before minting a duplicate. Keyed by
// submission id and searched on the ORIGINAL proposal (name + type),
// not the live-edited curation fields below, so editing the form
// causes no refetch. Mostly read-only: a provider row has no pick
// action here (adopt existing covers that, via its own SearchPicker),
// but a community row already carries its own product_id, so it gets
// a direct "Use as existing" shortcut. onAdopt is the panel's own
// adoptPick - the exact function the SearchPicker-driven community
// pick calls - so both entry points share one mutation and one
// pending/error/invalidation story; this component mints no pick or
// verdict logic of its own.
function PotentialDuplicates({
  submission,
  onAdopt,
  pending,
}: {
  submission: AdminSubmission
  onAdopt: (pick: CatalogPick) => void
  pending: boolean
}) {
  const search = useQuery({
    queryKey: ['admin', 'duplicates', submission.id],
    queryFn: () => searchCatalog(duplicatesKind(submission.item_type), submission.display_name),
  })
  if (search.isPending || search.isError) return null
  const rows = (search.data.results ?? []).slice(0, 5)
  return (
    <div className="mt-2 rounded border border-gray-200 p-2">
      <p className="text-xs font-semibold text-gray-500"><Trans>Potential duplicates</Trans></p>
      {rows.length === 0 ? (
        <p className="mt-1 text-sm text-gray-500"><Trans>None found.</Trans></p>
      ) : (
        <ul className="mt-1 flex flex-col gap-1">
          {rows.map((r, i) => (
            <li key={r.product_id ?? i} className="text-sm">
              {r.name}
              {releaseYear(r.first_release_date) && (
                <span className="ml-2 text-xs text-gray-400">{releaseYear(r.first_release_date)}</span>
              )}
              {(r.console_name ?? r.platform_name) && (
                <span className="ml-2 text-xs text-gray-400">{r.console_name ?? r.platform_name}</span>
              )}
              {r.origin === 'community' && (
                <span className="ml-2 rounded bg-indigo-100 px-1.5 py-0.5 text-xs font-semibold text-indigo-800">
                  <Trans>community</Trans>
                </span>
              )}
              {isExactDuplicate(submission, r) && (
                <span className="ml-2 rounded bg-amber-50 px-1.5 py-0.5 text-xs font-semibold text-amber-800">
                  <Trans>exact match</Trans>
                </span>
              )}
              {r.origin === 'community' && (
                <button
                  type="button"
                  onClick={() =>
                    onAdopt({
                      kind: 'community',
                      productId: r.product_id!,
                      name: r.name,
                      itemType: r.item_type ?? 'game',
                      platformName: r.platform_name,
                    })
                  }
                  disabled={pending}
                  className="ml-2 rounded border border-gray-300 px-2 py-0.5 text-xs hover:border-gray-400 hover:bg-gray-50 disabled:opacity-50"
                >
                  <Trans>Use as existing</Trans>
                </button>
              )}
            </li>
          ))}
        </ul>
      )}
    </div>
  )
}

// ReviewPanel is one submission's curation surface. The proposal
// pre-fills the mint form (moderated shared truth: the admin fixes
// casing, platform text, dates before anything becomes catalog).
// Adopt existing covers the second submitter and the
// provider-already-has-it case: community hits adopt directly,
// provider hits resolve to a product first. Reject carries the
// reason the submitter reads.
export default function ReviewPanel({ submission, onDone }: ReviewPanelProps) {
  const { i18n } = useLingui()
  const [name, setName] = useState(submission.display_name)
  const [itemType, setItemType] = useState(submission.item_type)
  const [platform, setPlatform] = useState<PlatformValue>({ platformName: submission.platform_name ?? '' })
  const [region, setRegion] = useState<string>(submission.region)
  const [edition, setEdition] = useState(submission.edition ?? '')
  const [releaseDate, setReleaseDate] = useState(submission.first_release_date ?? '')
  const [coverUrl, setCoverUrl] = useState(submission.cover_url ?? '')
  const [reason, setReason] = useState('')
  const [adopting, setAdopting] = useState(false)
  const [adoptId, setAdoptId] = useState('')
  const [resolveError, setResolveError] = useState(false)

  const verdict = useMutation({
    mutationFn: (v: VerdictRequest) => submitVerdict(submission.id, v),
    onSuccess: () => onDone(),
    onError: (e) => {
      // A raced verdict is done from this row's perspective: close, and
      // carry the error up so the queue can show it once this unmounts.
      if (e instanceof ApiError && e.code === 'submission_resolved') onDone(e)
    },
  })

  const approveNew = () =>
    verdict.mutate({
      action: 'approve_new',
      product: {
        type: itemType,
        name: name.trim(),
        // Name-only by design: community facts carry platform_name, never
        // a platform id, so a confirmed catalog pick's id is display-only
        // here and intentionally dropped from the mint.
        ...(platform.platformName.trim() !== '' && { platform_name: platform.platformName.trim() }),
        ...(region !== '' && { region }),
        ...(edition.trim() !== '' && { edition: edition.trim() }),
        ...(releaseDate !== '' && { first_release_date: releaseDate }),
        ...(coverUrl.trim() !== '' && { cover_url: coverUrl.trim() }),
      },
    })
  const adoptExisting = (productId: string) =>
    verdict.mutate({ action: 'approve_existing', product_id: productId })
  const adoptPick = (p: CatalogPick) => {
    setResolveError(false)
    if (p.kind === 'community') {
      adoptExisting(p.productId)
      return
    }
    resolveProduct(resolveRequestFor(p, undefined, undefined))
      .then((prod) => adoptExisting(prod.id))
      .catch(() => setResolveError(true))
  }

  const displayName = submission.display_name
  return (
    <div aria-label={t(i18n)`Review ${displayName}`} className="mt-3 rounded border border-gray-300 p-3">
      <h4 className="text-sm font-semibold"><Trans>Review: {displayName}</Trans></h4>
      <PotentialDuplicates submission={submission} onAdopt={adoptPick} pending={verdict.isPending} />
      <div className="mt-2 grid max-w-xl grid-cols-2 gap-2 text-sm">
        <label className="col-span-2">
          <Trans>Name</Trans>
          <input value={name} onChange={(e) => setName(e.target.value)} className="mt-0.5 w-full rounded border border-gray-300 px-2 py-1" />
        </label>
        <label>
          <Trans>Type</Trans>
          <select value={itemType} onChange={(e) => setItemType(e.target.value as AdminSubmission['item_type'])} className="mt-0.5 w-full rounded border border-gray-300 px-2 py-1">
            <option value="game">game</option>
            <option value="console">console</option>
            <option value="accessory">accessory</option>
          </select>
        </label>
        <PlatformPicker value={platform} onChange={setPlatform} />
        <label>
          <Trans>Region</Trans>
          <input value={region} onChange={(e) => setRegion(e.target.value)} className="mt-0.5 w-full rounded border border-gray-300 px-2 py-1" />
        </label>
        <label>
          <Trans>Edition or variant</Trans>
          <input value={edition} onChange={(e) => setEdition(e.target.value)} maxLength={128} className="mt-0.5 w-full rounded border border-gray-300 px-2 py-1" />
        </label>
        <label>
          <Trans>First release date</Trans>
          <input type="date" value={releaseDate} onChange={(e) => setReleaseDate(e.target.value)} className="mt-0.5 w-full rounded border border-gray-300 px-2 py-1" />
        </label>
        <label>
          <Trans>Cover image link</Trans>
          <input value={coverUrl} onChange={(e) => setCoverUrl(e.target.value)} placeholder="https://..." className="mt-0.5 w-full rounded border border-gray-300 px-2 py-1" />
        </label>
        {coverUrl.trim() !== '' && (
          <img
            key={coverUrl}
            src={coverUrl}
            alt={t(i18n)`cover preview`}
            className="col-span-2 h-24 w-auto rounded"
            onError={(e) => { e.currentTarget.style.display = 'none' }}
          />
        )}
      </div>
      <div className="mt-3 flex flex-wrap items-center gap-2 text-sm">
        <button
          type="button"
          onClick={approveNew}
          disabled={verdict.isPending || name.trim() === ''}
          className="rounded border border-gray-300 px-3 py-1 hover:bg-gray-50 disabled:opacity-50"
        >
          <Trans>Approve as new product</Trans>
        </button>
        <button
          type="button"
          onClick={() => setAdopting((v) => !v)}
          className="rounded border border-gray-300 px-3 py-1 hover:bg-gray-50"
        >
          <Trans>Adopt existing product</Trans>
        </button>
        <input
          aria-label={t(i18n)`Rejection reason`}
          value={reason}
          onChange={(e) => setReason(e.target.value)}
          placeholder={t(i18n)`Rejection reason`}
          className="w-64 rounded border border-gray-300 px-2 py-1"
        />
        <button
          type="button"
          onClick={() => verdict.mutate({ action: 'reject', reason: reason.trim() })}
          disabled={verdict.isPending || reason.trim() === ''}
          className="rounded border border-gray-300 px-3 py-1 hover:bg-gray-50 disabled:opacity-50"
        >
          <Trans>Reject</Trans>
        </button>
      </div>
      {adopting && (
        <div className="mt-3 border-t border-gray-200 pt-3">
          <SearchPicker kinds={['game', 'hardware']} initialQuery={submission.display_name} onPick={adoptPick} />
          <form
            className="mt-2 flex gap-2"
            onSubmit={(e) => {
              e.preventDefault()
              adoptExisting(adoptId.trim())
            }}
          >
            <input
              aria-label={t(i18n)`Product id`}
              value={adoptId}
              onChange={(e) => setAdoptId(e.target.value)}
              placeholder={t(i18n)`Product id (uuid)`}
              className="w-96 rounded border border-gray-300 px-2 py-1 text-sm"
            />
            <button
              type="submit"
              disabled={adoptId.trim() === '' || verdict.isPending}
              className="rounded border border-gray-300 px-3 py-1 text-sm hover:bg-gray-50 disabled:opacity-50"
            >
              <Trans>Adopt by id</Trans>
            </button>
          </form>
        </div>
      )}
      {resolveError && (
        <p role="alert" className="mt-2 text-sm text-red-700">
          <Trans>The pick could not be resolved to a product - try again or paste an id.</Trans>
        </p>
      )}
      {verdict.isError && (
        <p role="alert" className="mt-2 text-sm text-red-700">
          {verdictErrorMessage(verdict.error, i18n)}
        </p>
      )}
    </div>
  )
}
