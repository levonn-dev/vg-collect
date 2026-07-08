import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { createView, deleteView, fetchViews, updateView } from '../../api/collection'
import type { ListState } from '../../lib/listParams'
import { fromViewParams, toViewParams } from '../../lib/listParams'

interface ViewPickerProps {
  state: ListState
  onApply: (next: ListState) => void
}

// ViewPicker persists whole list states (filters, sort, group, mode)
// as saved views. The params JSON is the codec's serialization, so a
// view saved on one device restores identically on another.
export default function ViewPicker({ state, onApply }: ViewPickerProps) {
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
      if (!active) throw new Error('no active view')
      return updateView(active.id, active.name, toViewParams(state))
    },
    onSuccess: invalidate,
  })
  const remove = useMutation({
    mutationFn: () => {
      if (!state.viewId) throw new Error('no active view')
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

  return (
    <section aria-label="Saved views" className="mb-3 flex flex-wrap items-center gap-2">
      <label className="flex items-center gap-2 text-sm font-medium">
        Saved view
        <select
          value={state.viewId ?? ''}
          onChange={(e) => e.target.value && applyView(e.target.value)}
          className="rounded border border-gray-300 px-2 py-1 text-sm"
        >
          <option value="">Choose...</option>
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
          const name = window.prompt('Name this view')
          if (name?.trim()) save.mutate(name.trim())
        }}
        disabled={save.isPending}
        className="rounded border border-gray-300 px-2 py-1 text-sm enabled:hover:bg-gray-50 disabled:opacity-50"
      >
        Save view...
      </button>
      {state.viewId && (
        <>
          <button
            type="button"
            onClick={() => update.mutate()}
            disabled={update.isPending}
            className="rounded border border-gray-300 px-2 py-1 text-sm enabled:hover:bg-gray-50 disabled:opacity-50"
          >
            Update view
          </button>
          <button
            type="button"
            onClick={() => {
              if (window.confirm('Delete this saved view?')) remove.mutate()
            }}
            disabled={remove.isPending}
            className="rounded border border-gray-300 px-2 py-1 text-sm text-red-700 enabled:hover:bg-red-50 disabled:opacity-50"
          >
            Delete view
          </button>
        </>
      )}
      {error && (
        <p role="alert" className="text-xs text-red-700">
          {error.message || 'The view operation failed.'}
        </p>
      )}
    </section>
  )
}
