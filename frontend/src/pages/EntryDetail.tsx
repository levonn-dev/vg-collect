import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { useNavigate, useParams } from 'react-router'
import { ApiError } from '../api/client'
import type { EntryUpdate } from '../api/collection'
import { deleteEntry, fetchEntry, updateEntry } from '../api/collection'
import ItemTypeIcon from '../components/ItemTypeIcon'
import EntryForm from '../components/entry/EntryForm'
import PricingPanel from '../components/entry/PricingPanel'
import { releaseYear } from '../lib/format'

export default function EntryDetail() {
  const { id = '' } = useParams()
  const navigate = useNavigate()
  const queryClient = useQueryClient()
  const entry = useQuery({ queryKey: ['entry', id], queryFn: () => fetchEntry(id) })

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
      void navigate('/')
    },
  })

  if (entry.isPending) return <main className="py-8">Loading entry...</main>
  if (entry.isError) {
    if (entry.error instanceof ApiError && entry.error.status === 404) {
      return (
        <main className="py-8" role="alert">
          This entry does not exist (it may have been deleted).
        </main>
      )
    }
    return (
      <main className="py-8" role="alert">
        The entry cannot be loaded right now. Please try again.
      </main>
    )
  }

  const e = entry.data
  return (
    <main className="py-6" aria-label="Entry detail">
      <header className="mb-6 flex items-start gap-4">
        {e.cover_url ? (
          <img
            src={e.cover_url}
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
          <h2 className="text-2xl font-bold">{e.display_name}</h2>
          <p className="text-sm text-gray-600">
            {[e.platform?.name, releaseYear(e.first_release_date), e.item_type].filter(Boolean).join(' - ')}
            {!e.product_id && ' - custom item'}
          </p>
        </div>
        <button
          onClick={() => {
            if (window.confirm('Delete this entry? This cannot be undone.')) remove.mutate()
          }}
          disabled={remove.isPending}
          className="ml-auto rounded border border-red-300 px-3 py-1 text-sm text-red-700 hover:bg-red-50 disabled:opacity-50"
        >
          Delete entry
        </button>
      </header>
      <PricingPanel entry={e} />
      <EntryForm
        entry={e}
        onSave={(u) => save.mutate(u)}
        saving={save.isPending}
        error={save.isError ? save.error.message : null}
      />
    </main>
  )
}
