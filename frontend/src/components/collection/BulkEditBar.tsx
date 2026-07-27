import { useMutation, useQueryClient } from '@tanstack/react-query'
import { useState } from 'react'
import { ApiError } from '../../api/client'
import type { BulkUpdateRequest, Entry, Tag } from '../../api/collection'
import { bulkUpdateEntries } from '../../api/collection'
import { statusLabels } from './EntryTable'

// The server's own cap on entry_ids per request (api/bff.yaml); the
// bar disables Apply and explains itself once the shared selection
// grows past it rather than let the request fail server-side.
const SELECTION_CAP = 200

interface BulkEditBarProps {
  selected: ReadonlySet<string>
  tags: Tag[]
  onCancel: () => void
  // Fires only on a successful apply, with the response's updated_count
  // - Collection exits bulk mode, clears the selection, and announces
  // the count itself, so the bar does not need to survive its own
  // unmount to show that message.
  onApplied: (updatedCount: number) => void
}

// BulkEditBar is Collection's bulk-actions surface, mounted only while
// bulk mode is on. It owns its own draft (which tags to add/remove, an
// optional status, an optional storage location) and the single
// mutation that submits them together against the shared selection
// Set. Cancel and a successful apply both hand control back to
// Collection immediately; a FAILED apply deliberately does neither -
// the draft and the selection stay exactly as they were so a
// tag_cap_exceeded response (or any other 400) leaves the user able to
// adjust and retry without reselecting anything.
export default function BulkEditBar({ selected, tags, onCancel, onApplied }: BulkEditBarProps) {
  const queryClient = useQueryClient()
  const [addTagIds, setAddTagIds] = useState<string[]>([])
  const [removeTagIds, setRemoveTagIds] = useState<string[]>([])
  const [status, setStatus] = useState<Entry['status'] | ''>('')
  const [locationEnabled, setLocationEnabled] = useState(false)
  const [location, setLocation] = useState('')

  const apply = useMutation({
    mutationFn: (body: BulkUpdateRequest) => bulkUpdateEntries(body),
    onSuccess: (result) => {
      // Precisely the queries a bulk update can move: the list itself,
      // tag usage counts (add/remove both touch entry_count), the
      // dashboard stats a status change feeds (InsightsPanel/StatCards
      // - both keyed under 'dashboard'), and recommendation weights (a
      // status change alters them server-side - dropped halves a
      // game's weight - the same reason EntryDetail's single-entry
      // save invalidates 'recommendations' on every save).
      void queryClient.invalidateQueries({ queryKey: ['entries'] })
      void queryClient.invalidateQueries({ queryKey: ['tags'] })
      void queryClient.invalidateQueries({ queryKey: ['dashboard'] })
      void queryClient.invalidateQueries({ queryKey: ['recommendations'] })
      onApplied(result.updated_count)
    },
  })

  const toggleAddTag = (id: string) =>
    setAddTagIds((v) => (v.includes(id) ? v.filter((x) => x !== id) : [...v, id]))
  const toggleRemoveTag = (id: string) =>
    setRemoveTagIds((v) => (v.includes(id) ? v.filter((x) => x !== id) : [...v, id]))

  const hasAction = addTagIds.length > 0 || removeTagIds.length > 0 || status !== '' || locationEnabled
  const overCap = selected.size > SELECTION_CAP
  const disabled = apply.isPending

  const submit = () => {
    const body: BulkUpdateRequest = { entry_ids: [...selected] }
    if (addTagIds.length > 0) body.add_tag_ids = addTagIds
    if (removeTagIds.length > 0) body.remove_tag_ids = removeTagIds
    if (status !== '') body.status = status
    // Checked-with-empty-text clears the field (an explicit '' on the
    // wire); unchecked never sets the key at all, leaving it untouched
    // - the opposite of the single-entry form's clearing rule.
    if (locationEnabled) body.storage_location = location
    apply.mutate(body)
  }

  return (
    <section
      aria-label="Bulk edit"
      className="mb-3 flex flex-col gap-3 rounded border border-gray-300 bg-gray-50 p-3"
    >
      <div className="flex flex-wrap items-end gap-4">
        <span className="text-sm font-medium">{selected.size} selected</span>
        <fieldset disabled={disabled} className="flex flex-wrap items-center gap-2">
          <legend className="float-left mr-2 text-xs font-semibold uppercase tracking-wide text-gray-500">
            Add tags
          </legend>
          {tags.map((t) => (
            <label key={t.id} className="flex items-center gap-1 text-sm">
              <input type="checkbox" checked={addTagIds.includes(t.id)} onChange={() => toggleAddTag(t.id)} />
              {t.name}
            </label>
          ))}
          {tags.length === 0 && <span className="text-xs text-gray-400">No tags yet</span>}
        </fieldset>
        <fieldset disabled={disabled} className="flex flex-wrap items-center gap-2">
          <legend className="float-left mr-2 text-xs font-semibold uppercase tracking-wide text-gray-500">
            Remove tags
          </legend>
          {tags.map((t) => (
            <label key={t.id} className="flex items-center gap-1 text-sm">
              <input type="checkbox" checked={removeTagIds.includes(t.id)} onChange={() => toggleRemoveTag(t.id)} />
              {t.name}
            </label>
          ))}
          {tags.length === 0 && <span className="text-xs text-gray-400">No tags yet</span>}
        </fieldset>
        <label className="flex items-center gap-2 text-sm font-medium">
          Status
          <select
            value={status}
            disabled={disabled}
            onChange={(e) => setStatus(e.target.value as Entry['status'] | '')}
            className="rounded border border-gray-300 px-2 py-1 text-sm"
          >
            <option value="">Leave unchanged</option>
            {(Object.keys(statusLabels) as Entry['status'][]).map((s) => (
              <option key={s} value={s}>
                {statusLabels[s]}
              </option>
            ))}
          </select>
        </label>
        <div className="flex flex-col gap-1">
          <label className="flex items-center gap-2 text-sm">
            <input
              type="checkbox"
              checked={locationEnabled}
              disabled={disabled}
              onChange={(e) => setLocationEnabled(e.target.checked)}
            />
            Set storage location
          </label>
          {locationEnabled && (
            <>
              <input
                aria-label="Storage location"
                value={location}
                disabled={disabled}
                maxLength={200}
                onChange={(e) => setLocation(e.target.value)}
                className="rounded border border-gray-300 px-2 py-1 text-sm"
              />
              <span className="text-xs text-gray-500">Empty clears the location.</span>
            </>
          )}
        </div>
      </div>
      {overCap && (
        <p className="text-sm text-amber-800">Selection is over the 200-entry limit.</p>
      )}
      {apply.isError && (
        <p role="alert" className="text-sm text-red-700">
          {apply.error instanceof ApiError && apply.error.message
            ? apply.error.message
            : 'The bulk update failed.'}
        </p>
      )}
      <div className="flex items-center gap-2">
        <button
          type="button"
          onClick={submit}
          disabled={disabled || !hasAction || selected.size === 0 || overCap}
          className="rounded bg-gray-900 px-3 py-1 text-sm text-white disabled:opacity-50"
        >
          Apply
        </button>
        <button
          type="button"
          onClick={onCancel}
          disabled={disabled}
          className="rounded border border-gray-300 px-3 py-1 text-sm enabled:hover:bg-gray-50 disabled:opacity-50"
        >
          Cancel
        </button>
      </div>
    </section>
  )
}
