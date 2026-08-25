import { Trans, useLingui } from '@lingui/react/macro'
import { msg, t } from '@lingui/core/macro'
import type { I18n, MessageDescriptor } from '@lingui/core'
import { useMutation, useQuery } from '@tanstack/react-query'
import { useState } from 'react'
import { submitVerdict } from '../../api/admin'
import type { AdminSubmission, VerdictRequest } from '../../api/admin'
import type { SearchKind, SearchResult } from '../../api/catalog'
import { resolveProduct, searchCatalog } from '../../api/catalog'
import { ApiError } from '../../api/client'
import { CommunityProductSpec } from '../../gen/facets'
import { itemTypeValues } from '../../api/schema'
import { resolveRequestFor } from '../../lib/catalog'
import { releaseYear } from '../../lib/format'
import { cleanNames } from '../../lib/credits'
import { itemTypeWireLabels } from '../../lib/enumLabels'
import { btnSecondary } from '../../lib/formStyles'
import { resolveApiError } from '../../lib/resolveApiError'
import StringListInput from '../StringListInput'
import PlatformPicker from '../catalog/PlatformPicker'
import type { PlatformValue } from '../catalog/PlatformPicker'
import RegionPicker from '../catalog/RegionPicker'
import SearchPicker from '../catalog/SearchPicker'
import type { CatalogPick } from '../../lib/catalogPicks'

const ITEM_TYPE_VALUES = itemTypeValues
const SUBMISSION_NAME_MAX = CommunityProductSpec.properties.name.maxLength
const SUBMISSION_EDITION_MAX = CommunityProductSpec.properties.edition.maxLength
const SUBMISSION_PLATFORM_MAX = CommunityProductSpec.properties.platform_name.maxLength
const SUBMISSION_COVER_URL_MAX = CommunityProductSpec.properties.cover_url.maxLength

interface ReviewPanelProps {
  submission: AdminSubmission
  // Closes the panel; an optional error rides to the queue notice so a raced
  // verdict is explained after unmount. The error travels, not a rendered
  // message, so the notice follows a later locale switch.
  onDone: (error?: ApiError) => void
}

const verdictErrorCodes: Record<string, MessageDescriptor> = {
  submission_resolved: msg`Another admin already resolved this submission.`,
  unknown_product: msg`No product with that id.`,
  enrichment_unavailable: msg`The catalog cannot be reached - try again.`,
}

// Uses explicit t(i18n), not useLingui()'s t, to match resolveApiError's
// signature without importing a second same-named t. PotentialDuplicates below
// has no such collision, so it uses the plain useLingui()-bound Trans/t.
// eslint-disable-next-line react-refresh/only-export-components -- shared with SubmissionsQueue, which phrases the error this panel hands it, alongside this component.
export function verdictErrorMessage(e: unknown, i18n: I18n): string {
  return resolveApiError(e, i18n, verdictErrorCodes, msg`The verdict failed.`)
}

function duplicatesKind(itemType: AdminSubmission['item_type']): SearchKind {
  return itemType === 'game' ? 'game' : 'hardware'
}

// Case/whitespace-insensitive name match, plus platform match when both
// sides have one; a missing platform falls back to name-only (a data gap
// isn't a mismatch signal).
function isExactDuplicate(submission: AdminSubmission, row: SearchResult): boolean {
  const same = (a: string, b: string) => a.trim().toLowerCase() === b.trim().toLowerCase()
  if (!same(row.name, submission.display_name)) return false
  const rowPlatform = row.console_name ?? row.platform_name
  const subPlatform = submission.platform_name
  if (!rowPlatform || !subPlatform) return true
  return same(rowPlatform, subPlatform)
}

// Searched on the ORIGINAL proposal (name + type), not the live-edited fields
// below, so editing the form causes no refetch.
// Community rows get a "Use as existing" shortcut (they carry product_id);
// provider rows have no pick action here (SearchPicker's adopt-existing covers it).
// onAdopt is the panel's own adoptPick, so both entry points share one mutation.
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

// Proposal pre-fills the mint form so the admin can fix casing/platform/dates
// before anything becomes catalog. Adopt existing: community hits adopt
// directly, provider resolves to a product first. Reject carries a reason.
export default function ReviewPanel({ submission, onDone }: ReviewPanelProps) {
  const { i18n } = useLingui()
  const [name, setName] = useState(submission.display_name)
  const [itemType, setItemType] = useState(submission.item_type)
  const [platform, setPlatform] = useState<PlatformValue>({ platformName: submission.platform_name ?? '' })
  const [region, setRegion] = useState<string>(submission.region)
  const [edition, setEdition] = useState(submission.edition ?? '')
  const [releaseDate, setReleaseDate] = useState(submission.first_release_date ?? '')
  const [coverUrl, setCoverUrl] = useState(submission.cover_url ?? '')
  const [developers, setDevelopers] = useState<string[]>(submission.developers ?? [])
  const [publishers, setPublishers] = useState<string[]>(submission.publishers ?? [])
  const [reason, setReason] = useState('')
  const [adopting, setAdopting] = useState(false)
  const [adoptId, setAdoptId] = useState('')
  const [resolveError, setResolveError] = useState(false)

  const verdict = useMutation({
    mutationFn: (v: VerdictRequest) => submitVerdict(submission.id, v),
    onSuccess: () => onDone(),
    onError: (e) => {
      // A raced verdict closes this row; the error carries up for the queue to show.
      if (e instanceof ApiError && e.code === 'submission_resolved') onDone(e)
    },
  })

  const approveNew = () =>
    verdict.mutate({
      action: 'approve_new',
      product: {
        type: itemType,
        name: name.trim(),
        // Name-only by design: community facts carry platform_name, never an
        // id, so a confirmed pick's id is display-only and dropped from the mint.
        ...(platform.platformName.trim() !== '' && { platform_name: platform.platformName.trim() }),
        ...(region !== '' && { region }),
        ...(edition.trim() !== '' && { edition: edition.trim() }),
        ...(releaseDate !== '' && { first_release_date: releaseDate }),
        ...(cleanNames(developers) !== undefined && { developers: cleanNames(developers) }),
        ...(cleanNames(publishers) !== undefined && { publishers: cleanNames(publishers) }),
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
          <input value={name} onChange={(e) => setName(e.target.value)} maxLength={SUBMISSION_NAME_MAX} className="mt-0.5 w-full rounded border border-gray-300 px-2 py-1" />
        </label>
        <label>
          <Trans>Type</Trans>
          <select value={itemType} onChange={(e) => setItemType(e.target.value as AdminSubmission['item_type'])} className="mt-0.5 w-full rounded border border-gray-300 px-2 py-1">
            {ITEM_TYPE_VALUES.map((v) => (
              <option key={v} value={v}>{i18n._(itemTypeWireLabels[v])}</option>
            ))}
          </select>
        </label>
        <PlatformPicker value={platform} onChange={setPlatform} maxLength={SUBMISSION_PLATFORM_MAX} />
        <RegionPicker value={region} onChange={setRegion} />
        <label>
          <Trans>Edition or variant</Trans>
          <input value={edition} onChange={(e) => setEdition(e.target.value)} maxLength={SUBMISSION_EDITION_MAX} className="mt-0.5 w-full rounded border border-gray-300 px-2 py-1" />
        </label>
        <label>
          <Trans>First release date</Trans>
          <input type="date" value={releaseDate} onChange={(e) => setReleaseDate(e.target.value)} className="mt-0.5 w-full rounded border border-gray-300 px-2 py-1" />
        </label>
        <div className="col-span-2 grid grid-cols-2 gap-2">
          <StringListInput label={t(i18n)`Developers`} addLabel={t(i18n)`Add developer`}
            values={developers} onChange={setDevelopers} />
          <StringListInput label={t(i18n)`Publishers`} addLabel={t(i18n)`Add publisher`}
            values={publishers} onChange={setPublishers} />
        </div>
        <label>
          <Trans>Cover image link</Trans>
          <input
            value={coverUrl}
            onChange={(e) => setCoverUrl(e.target.value)}
            maxLength={SUBMISSION_COVER_URL_MAX}
            placeholder={t(i18n)`https://...`}
            className="mt-0.5 w-full rounded border border-gray-300 px-2 py-1"
          />
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
          className={btnSecondary}
        >
          <Trans>Approve as new product</Trans>
        </button>
        <button
          type="button"
          onClick={() => setAdopting((v) => !v)}
          className={btnSecondary}
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
          className={btnSecondary}
        >
          <Trans>Reject</Trans>
        </button>
      </div>
      {adopting && (
        <div className="mt-3 border-t border-gray-200 pt-3">
          <SearchPicker
            kinds={['game', 'hardware']}
            // Seeds via the same initialState seam AddWizard uses; kind matches
            // the submission's item type instead of defaulting to game.
            initialState={{
              kind: duplicatesKind(submission.item_type),
              text: submission.display_name,
              submitted: submission.display_name.trim(),
            }}
            onPick={adoptPick}
          />
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
              className={btnSecondary}
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
