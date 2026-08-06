import { Trans, useLingui } from '@lingui/react/macro'
import { msg } from '@lingui/core/macro'
import type { MessageDescriptor } from '@lingui/core'
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { useLocation, useNavigate, useParams } from 'react-router'
import { ApiError } from '../api/client'
import type { EntryUpdate } from '../api/collection'
import { deleteEntry, fetchEntry, updateEntry } from '../api/collection'
import ItemTypeIcon from '../components/ItemTypeIcon'
import ApprovalNotice from '../components/entry/ApprovalNotice'
import CatalogSubmission from '../components/entry/CatalogSubmission'
import EntryForm from '../components/entry/EntryForm'
import { releaseYear } from '../lib/format'
import { entryCover, entrySecondary, entryTitle, entryTitleLang, titleFormFor } from '../lib/productTitle'

// Identity-preserving: the byline has never been prettified, so the
// item-type word stays the raw wire value. An unknown future wire
// value falls back to rendering itself raw.
const itemTypeLabels: Record<string, MessageDescriptor> = {
  game: msg`game`,
  console: msg`console`,
  accessory: msg`accessory`,
}

export default function EntryDetail() {
  const { t, i18n } = useLingui()
  const form = titleFormFor(i18n.locale)
  const { id = '' } = useParams()
  const navigate = useNavigate()
  const location = useLocation()
  const queryClient = useQueryClient()
  const entry = useQuery({ queryKey: ['entry', id], queryFn: () => fetchEntry(id) })
  // The wizard lands here with navigation state after a create; the
  // banner confirms the add without a layout jump elsewhere.
  const justAdded = Boolean((location.state as { justAdded?: boolean } | null)?.justAdded)

  const save = useMutation({
    mutationFn: (update: EntryUpdate) => updateEntry(id, update),
    onSuccess: (updated) => {
      queryClient.setQueryData(['entry', id], updated)
      void queryClient.invalidateQueries({ queryKey: ['entries'] })
      void queryClient.invalidateQueries({ queryKey: ['dashboard'] })
      void queryClient.invalidateQueries({ queryKey: ['recommendations'] })
    },
  })
  const remove = useMutation({
    mutationFn: () => deleteEntry(id),
    onSuccess: () => {
      void queryClient.invalidateQueries({ queryKey: ['entries'] })
      void queryClient.invalidateQueries({ queryKey: ['dashboard'] })
      void queryClient.invalidateQueries({ queryKey: ['recommendations'] })
      queryClient.removeQueries({ queryKey: ['entry', id] })
      void navigate('/collection')
    },
  })

  if (entry.isPending) return <main className="py-8"><Trans>Loading entry...</Trans></main>
  if (entry.isError) {
    if (entry.error instanceof ApiError && entry.error.status === 404) {
      return (
        <main className="py-8" role="alert">
          <Trans>This entry does not exist (it may have been deleted).</Trans>
        </main>
      )
    }
    return (
      <main className="py-8" role="alert">
        <Trans>The entry cannot be loaded right now. Please try again.</Trans>
      </main>
    )
  }

  const e = entry.data
  const cover = entryCover(e)
  return (
    <main className="py-6" aria-label={t`Entry detail`}>
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
            <p className="text-sm text-gray-500">{entrySecondary(e, form)}</p>
          )}
          <p className="text-sm text-gray-600">
            {[
              e.platform?.name,
              releaseYear(e.first_release_date),
              itemTypeLabels[e.item_type] ? i18n._(itemTypeLabels[e.item_type]) : e.item_type,
            ].filter(Boolean).join(' - ')}
            {!e.product_id && <Trans> - custom item</Trans>}
          </p>
        </div>
        <button
          onClick={() => {
            if (window.confirm(t`Delete this entry? This cannot be undone.`)) remove.mutate()
          }}
          disabled={remove.isPending}
          className="ml-auto rounded border border-red-300 px-3 py-1 text-sm text-red-700 hover:bg-red-50 disabled:opacity-50"
        >
          <Trans>Delete entry</Trans>
        </button>
      </header>
      {e.product_id && <ApprovalNotice entryId={e.id} />}
      {!e.product_id && <CatalogSubmission entryId={e.id} />}
      <EntryForm
        entry={e}
        onSave={(u) => save.mutate(u)}
        saving={save.isPending}
        saved={save.isSuccess}
        error={save.isError ? save.error.message : null}
      />
    </main>
  )
}
