import { Plural, Trans, useLingui } from '@lingui/react/macro'
import { msg } from '@lingui/core/macro'
import type { I18n, MessageDescriptor } from '@lingui/core'
import { useMutation, useQueryClient } from '@tanstack/react-query'
import { useState } from 'react'
import type { BulkUpdateRequest, Entry, Tag } from '../../api/collection'
import { bulkUpdateEntries } from '../../api/collection'
import { BulkUpdateRequest as BulkUpdateRequestFacet } from '../../gen/facets'
import { statusLabels } from '../../lib/enumLabels'
import { invalidateEntryQueries } from '../../lib/entryQueries'
import { btnSecondary } from '../../lib/formStyles'
import { resolveApiError } from '../../lib/resolveApiError'
import SectionLabel from '../SectionLabel'

// Server's cap on entry_ids per request; Apply disables and explains itself
// past it rather than fail server-side. Named `cap` (not the facet's own
// name) so it doubles as the over-cap message's interpolation placeholder.
const cap = BulkUpdateRequestFacet.properties.entry_ids.maxItems
const STORAGE_LOCATION_MAX = BulkUpdateRequestFacet.properties.storage_location.maxLength

// The only per-entry-tag-cap code the transaction can answer; everything else
// 400s as invalid_body, whose detail text already explains itself.
const bulkUpdateErrorCodes: Record<string, MessageDescriptor> = {
  tag_cap_exceeded: msg`One or more of the selected entries would end up with too many tags.`,
}

function bulkUpdateErrorMessage(e: unknown, i18n: I18n): string {
  return resolveApiError(e, i18n, bulkUpdateErrorCodes, msg`The bulk update failed.`)
}

interface BulkEditBarProps {
  selected: ReadonlySet<string>
  tags: Tag[]
  onCancel: () => void
  // Fires only on success, with updated_count; Collection announces the
  // count itself so the bar doesn't need to survive its own unmount.
  onApplied: (updatedCount: number) => void
}

// Cancel and a successful apply hand control back to Collection immediately;
// a failed apply does neither, so the draft/selection survive for a retry
// (e.g. tag_cap_exceeded) without reselecting.
export default function BulkEditBar({ selected, tags, onCancel, onApplied }: BulkEditBarProps) {
  const { t, i18n } = useLingui()
  const queryClient = useQueryClient()
  const [addTagIds, setAddTagIds] = useState<string[]>([])
  const [removeTagIds, setRemoveTagIds] = useState<string[]>([])
  const [status, setStatus] = useState<Entry['status'] | ''>('')
  const [locationEnabled, setLocationEnabled] = useState(false)
  const [location, setLocation] = useState('')

  const apply = useMutation({
    mutationFn: (body: BulkUpdateRequest) => bulkUpdateEntries(body),
    onSuccess: (result) => {
      // Invalidates: the list, tag usage counts (entry_count), dashboard stats
      // ('dashboard'), and recommendation weights (a status change alters them
      // server-side, e.g. dropped halves a game's weight).
      invalidateEntryQueries(queryClient, [['tags']])
      onApplied(result.updated_count)
    },
  })

  // add_tag_ids/remove_tag_ids sharing an id would be self-contradictory, so
  // checking one unchecks the other. The sibling clear reads current state
  // outside either updater, as its own setState - updaters stay pure.
  const toggleAddTag = (id: string) => {
    const adding = !addTagIds.includes(id)
    setAddTagIds((v) => (adding ? [...v, id] : v.filter((x) => x !== id)))
    if (adding) setRemoveTagIds((r) => r.filter((x) => x !== id))
  }
  const toggleRemoveTag = (id: string) => {
    const adding = !removeTagIds.includes(id)
    setRemoveTagIds((v) => (adding ? [...v, id] : v.filter((x) => x !== id)))
    if (adding) setAddTagIds((a) => a.filter((x) => x !== id))
  }

  const hasAction = addTagIds.length > 0 || removeTagIds.length > 0 || status !== '' || locationEnabled
  const overCap = selected.size > cap
  const disabled = apply.isPending

  const submit = () => {
    const body: BulkUpdateRequest = { entry_ids: [...selected] }
    if (addTagIds.length > 0) body.add_tag_ids = addTagIds
    if (removeTagIds.length > 0) body.remove_tag_ids = removeTagIds
    if (status !== '') body.status = status
    // Checked+empty clears the field (explicit '' on the wire); unchecked
    // never sets the key - opposite of the single-entry form's rule.
    if (locationEnabled) body.storage_location = location
    apply.mutate(body)
  }

  const selectedCount = selected.size
  return (
    <section
      aria-label={t`Bulk edit`}
      className="mb-3 flex flex-col gap-3 rounded border border-gray-300 bg-gray-50 p-3"
    >
      <div className="flex flex-wrap items-end gap-4">
        <span className="text-sm font-medium">
          <Plural value={selectedCount} one="# selected" other="# selected" />
        </span>
        <fieldset disabled={disabled} className="flex flex-wrap items-center gap-2">
          <SectionLabel as="legend" size="xs" className="float-left mr-2">
            <Trans>Add tags</Trans>
          </SectionLabel>
          {tags.map((tag) => (
            <label key={tag.id} className="flex items-center gap-1 text-sm">
              <input type="checkbox" checked={addTagIds.includes(tag.id)} onChange={() => toggleAddTag(tag.id)} />
              {tag.name}
            </label>
          ))}
          {tags.length === 0 && <span className="text-xs text-gray-400"><Trans>No tags yet</Trans></span>}
        </fieldset>
        <fieldset disabled={disabled} className="flex flex-wrap items-center gap-2">
          <SectionLabel as="legend" size="xs" className="float-left mr-2">
            <Trans>Remove tags</Trans>
          </SectionLabel>
          {tags.map((tag) => (
            <label key={tag.id} className="flex items-center gap-1 text-sm">
              <input type="checkbox" checked={removeTagIds.includes(tag.id)} onChange={() => toggleRemoveTag(tag.id)} />
              {tag.name}
            </label>
          ))}
          {tags.length === 0 && <span className="text-xs text-gray-400"><Trans>No tags yet</Trans></span>}
        </fieldset>
        <label className="flex items-center gap-2 text-sm font-medium">
          <Trans>Status</Trans>
          <select
            value={status}
            disabled={disabled}
            onChange={(e) => setStatus(e.target.value as Entry['status'] | '')}
            className="rounded border border-gray-300 px-2 py-1 text-sm"
          >
            <option value="">{t`Leave unchanged`}</option>
            {(Object.keys(statusLabels) as Entry['status'][]).map((s) => (
              <option key={s} value={s}>
                {i18n._(statusLabels[s])}
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
            <Trans>Set storage location</Trans>
          </label>
          {locationEnabled && (
            <>
              <input
                aria-label={t`Storage location`}
                value={location}
                disabled={disabled}
                maxLength={STORAGE_LOCATION_MAX}
                onChange={(e) => setLocation(e.target.value)}
                className="rounded border border-gray-300 px-2 py-1 text-sm"
              />
              <span className="text-xs text-gray-500"><Trans>Empty clears the location.</Trans></span>
            </>
          )}
        </div>
      </div>
      {overCap && (
        <p className="text-sm text-amber-800"><Trans>Selection is over the {cap}-entry limit.</Trans></p>
      )}
      {apply.isError && (
        <p role="alert" className="text-sm text-red-700">
          {bulkUpdateErrorMessage(apply.error, i18n)}
        </p>
      )}
      <div className="flex items-center gap-2">
        <button
          type="button"
          onClick={submit}
          disabled={disabled || !hasAction || selected.size === 0 || overCap}
          className="rounded bg-gray-900 px-3 py-1 text-sm text-white disabled:opacity-50"
        >
          <Trans>Apply</Trans>
        </button>
        <button
          type="button"
          onClick={onCancel}
          disabled={disabled}
          className={btnSecondary}
        >
          <Trans>Cancel</Trans>
        </button>
      </div>
    </section>
  )
}
