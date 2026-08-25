import { Trans, useLingui } from '@lingui/react/macro'
import { msg } from '@lingui/core/macro'
import type { I18n, MessageDescriptor } from '@lingui/core'
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { useLocation, useNavigate, useParams } from 'react-router'
import { ApiError } from '../api/client'
import type { EntryUpdate } from '../api/collection'
import { deleteEntry, fetchEntry, updateEntry } from '../api/collection'
import ItemTypeIcon from '../components/ItemTypeIcon'
import ApprovalNotice from '../components/entry/ApprovalNotice'
import CatalogSubmission from '../components/entry/CatalogSubmission'
import EntryForm from '../components/entry/EntryForm'
import RegionMismatchBanner from '../components/entry/RegionMismatchBanner'
import { confirmThen } from '../lib/confirm'
import { itemTypeWireLabels } from '../lib/enumLabels'
import { invalidateEntryQueries } from '../lib/entryQueries'
import { releaseYear } from '../lib/format'
import { refetchWarning, renderQueryState } from '../lib/queryBoundary'
import { entryCover, entrySecondary, entrySecondaryLang, entryTitle, entryTitleLang, titleFormFor } from '../lib/productTitle'
import { resolveApiError } from '../lib/resolveApiError'
import { useDocumentTitle } from '../lib/useDocumentTitle'

// invalid_product_change omitted: EntryForm has no product-repoint
// control (only ConfirmStep does), so a save never triggers it.
const saveEntryErrorCodes: Record<string, MessageDescriptor> = {
  entry_not_found: msg`This entry no longer exists.`,
  unknown_pricing_product: msg`That price source no longer exists in the catalog.`,
  tag_not_found: msg`One of the selected tags no longer exists.`,
  enrichment_unavailable: msg`The catalog cannot be reached - try again.`,
}
function saveEntryErrorMessage(e: unknown, i18n: I18n): string {
  return resolveApiError(e, i18n, saveEntryErrorCodes, msg`The entry could not be saved.`)
}

export default function EntryDetail() {
  const { t, i18n } = useLingui()
  const form = titleFormFor(i18n.locale)
  const { id = '' } = useParams()
  const navigate = useNavigate()
  const location = useLocation()
  const queryClient = useQueryClient()
  const entry = useQuery({ queryKey: ['entry', id], queryFn: () => fetchEntry(id) })
  useDocumentTitle(entry.data ? entryTitle(entry.data, form) : t`Entry`)
  // Wizard lands here with navigation state after a create; banner
  // confirms without a layout jump.
  const justAdded = Boolean((location.state as { justAdded?: boolean } | null)?.justAdded)

  const save = useMutation({
    mutationFn: (update: EntryUpdate) => updateEntry(id, update),
    onSuccess: (updated) => {
      queryClient.setQueryData(['entry', id], updated)
      invalidateEntryQueries(queryClient)
    },
  })
  const remove = useMutation({
    mutationFn: () => deleteEntry(id),
    onSuccess: () => {
      invalidateEntryQueries(queryClient)
      queryClient.removeQueries({ queryKey: ['entry', id] })
      void navigate('/collection')
    },
  })

  if (entry.isPending || (entry.isError && entry.data === undefined)) {
    return renderQueryState(entry, {
      size: 'page',
      role: 'alert',
      loading: <Trans>Loading entry...</Trans>,
      error: <Trans>The entry cannot be loaded right now. Please try again.</Trans>,
      notFound: entry.isError && entry.error instanceof ApiError && entry.error.status === 404
        ? <main id="main-content" tabIndex={-1} className="py-8"><p role="alert"><Trans>This entry does not exist (it may have been deleted).</Trans></p></main>
        : undefined,
    })
  }

  const e = entry.data
  const cover = entryCover(e)
  // Entry's own credit snapshot (IGDB role split, community gap-fill,
  // or custom facts; server derives it).
  const developerNames = (e.developers ?? []).join(', ')
  const publisherNames = (e.publishers ?? []).join(', ')
  return (
    <main id="main-content" tabIndex={-1} className="py-6" aria-label={t`Entry detail`}>
      {refetchWarning(entry)}
      {justAdded && (
        <p role="status" className="mb-4 rounded bg-green-50 p-3 text-sm text-green-800">
          <Trans>Added to your collection.</Trans>
        </p>
      )}
      <header className="mb-6 flex items-start gap-4">
        {cover ? (
          <img
            src={cover}
            alt=""
            // Hardware images are platform logos: contain, never crop.
            className={e.item_type === 'game' ? 'h-24 w-auto rounded shadow' : 'h-24 w-24 rounded bg-gray-50 object-contain p-1'}
          />
        ) : (
          <div aria-hidden="true" className="flex h-24 w-16 items-center justify-center rounded bg-gray-100 text-gray-400">
            <ItemTypeIcon type={e.item_type} className="h-8 w-8" />
          </div>
        )}
        <div>
          <h2 className="text-2xl font-bold">
            <span lang={entryTitleLang(e, form)}>{entryTitle(e, form)}</span>
          </h2>
          {entrySecondary(e, form) && (
            <p className="text-sm text-gray-500" lang={entrySecondaryLang(e, form)}>{entrySecondary(e, form)}</p>
          )}
          <p className="text-sm text-gray-600">
            {[
              e.platform?.name,
              releaseYear(e.first_release_date),
              itemTypeWireLabels[e.item_type] ? i18n._(itemTypeWireLabels[e.item_type]) : e.item_type,
            ].filter(Boolean).join(' - ')}
            {!e.product_id && <Trans> - custom item</Trans>}
          </p>
          {(developerNames !== '' || publisherNames !== '') && (
            <p className="text-sm text-gray-600">
              {developerNames !== '' && (
                <span><Trans>Developed by {developerNames}</Trans></span>
              )}
              {developerNames !== '' && publisherNames !== '' && ' - '}
              {publisherNames !== '' && (
                <span><Trans>Published by {publisherNames}</Trans></span>
              )}
            </p>
          )}
        </div>
        <button
          type="button"
          onClick={() => confirmThen(t`Delete this entry? This cannot be undone.`, () => remove.mutate())}
          disabled={remove.isPending}
          className="ml-auto rounded border border-red-300 px-3 py-1 text-sm text-red-700 hover:bg-red-50 disabled:opacity-50"
        >
          <Trans>Delete entry</Trans>
        </button>
      </header>
      {e.product_id && (
        <>
          <ApprovalNotice entryId={e.id} />
          <RegionMismatchBanner
            entryId={e.id}
            productId={e.product_id}
            region={e.region}
            regionMismatchAckAt={e.region_mismatch_ack_at}
          />
        </>
      )}
      {!e.product_id && <CatalogSubmission entryId={e.id} />}
      <EntryForm
        entry={e}
        onSave={(u) => save.mutate(u)}
        saving={save.isPending}
        saved={save.isSuccess}
        error={save.isError ? saveEntryErrorMessage(save.error, i18n) : null}
      />
    </main>
  )
}
