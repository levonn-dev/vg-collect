import { Trans, useLingui } from '@lingui/react/macro'
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { createView, deleteView, fetchViews, updateView } from '../../api/collection'
import { confirmThen } from '../../lib/confirm'
import { btnSecondary } from '../../lib/formStyles'
import type { ListState } from '../../lib/listParams'
import { fromViewParams, toViewParams } from '../../lib/listParams'

interface ViewPickerProps {
  state: ListState
  onApply: (next: ListState) => void
}

// ViewPicker persists whole list states (filters, sort, group, mode)
// as shelves: pick a saved shelf, save the current state as a new
// one, or update/delete the active one. The params JSON is the
// codec's serialization, so a shelf saved on one device restores
// identically on another. This is the quick-row only - the per-shelf
// management list (badge, VisibilityControl, copy-link button) lives
// in ShelfManager, the Shelves tab's own component, which runs its
// own me/views queries rather than receiving this component's. The
// API still calls these views (SavedView, fetchViews, toViewParams,
// ...); this component's copy calls them shelves.
export default function ViewPicker({ state, onApply }: ViewPickerProps) {
  const { t } = useLingui()
  const queryClient = useQueryClient()
  const views = useQuery({ queryKey: ['views'], queryFn: fetchViews })
  const invalidate = () => void queryClient.invalidateQueries({ queryKey: ['views'] })

  const save = useMutation({
    mutationFn: (name: string) => createView(name, toViewParams(state)),
    onSuccess: (v) => {
      invalidate()
      onApply({ ...state, viewId: v.id })
    },
  })
  const update = useMutation({
    mutationFn: () => {
      const active = views.data?.find((v) => v.id === state.viewId)
      if (!active) throw new Error('no active shelf')
      // The full-replacement PUT carries the shelf's OWN existing
      // visibility forward - omitting it would default the row back
      // to private on every plain "Update shelf" click.
      return updateView(active.id, active.name, toViewParams(state), active.visibility)
    },
    onSuccess: invalidate,
  })
  const remove = useMutation({
    mutationFn: () => {
      if (!state.viewId) throw new Error('no active shelf')
      return deleteView(state.viewId)
    },
    onSuccess: () => {
      invalidate()
      onApply({ ...state, viewId: undefined })
    },
  })
  const applyView = (id: string) => {
    const v = views.data?.find((view) => view.id === id)
    if (!v) return
    onApply({ ...fromViewParams(v.params), viewId: v.id })
  }

  const error = save.error ?? update.error ?? remove.error
  // Each mutation's error otherwise lingers until that same mutation
  // fires again, so an unrelated later success would still show the
  // previous failure - reset all three before any new action starts.
  const resetErrors = () => {
    save.reset()
    update.reset()
    remove.reset()
  }

  return (
    <section aria-label={t`Shelf picker`} className="mb-3 flex flex-col gap-2">
      <div className="flex flex-wrap items-center gap-2">
        <label className="flex items-center gap-2 text-sm font-medium">
          <Trans>Shelf</Trans>
          <select
            value={state.viewId ?? ''}
            onChange={(e) => e.target.value && applyView(e.target.value)}
            className="rounded border border-gray-300 px-2 py-1 text-sm"
          >
            <option value="">{t`Choose...`}</option>
            {views.data?.map((v) => (
              <option key={v.id} value={v.id}>
                {v.name}
              </option>
            ))}
          </select>
        </label>
        <button
          type="button"
          onClick={() => {
            const name = window.prompt(t`Name this shelf`)
            if (name?.trim()) {
              resetErrors()
              save.mutate(name.trim())
            }
          }}
          disabled={save.isPending}
          className={btnSecondary}
        >
          <Trans>Save shelf...</Trans>
        </button>
        {state.viewId && (
          <>
            <button
              type="button"
              onClick={() => {
                resetErrors()
                update.mutate()
              }}
              disabled={update.isPending}
              className={btnSecondary}
            >
              <Trans>Update shelf</Trans>
            </button>
            <button
              type="button"
              onClick={() =>
                confirmThen(t`Delete this shelf?`, () => {
                  resetErrors()
                  remove.mutate()
                })
              }
              disabled={remove.isPending}
              className="rounded border border-gray-300 px-3 py-1 text-sm text-red-700 enabled:hover:bg-red-50 disabled:opacity-50"
            >
              <Trans>Delete shelf</Trans>
            </button>
          </>
        )}
        {error && (
          <p role="alert" className="text-xs text-red-700">
            {error.message || t`The shelf operation failed.`}
          </p>
        )}
      </div>
    </section>
  )
}
