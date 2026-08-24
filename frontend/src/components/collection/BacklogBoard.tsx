import { useLingui } from '@lingui/react/macro'
import { closestCenter, DndContext, PointerSensor, useSensor, useSensors } from '@dnd-kit/core'
import type { DragEndEvent } from '@dnd-kit/core'
import { SortableContext, useSortable, verticalListSortingStrategy } from '@dnd-kit/sortable'
import { CSS } from '@dnd-kit/utilities'
import { useMutation, useQueryClient } from '@tanstack/react-query'
import { useState } from 'react'
import { Link } from 'react-router'
import { ApiError } from '../../api/client'
import type { Entry, EntryList } from '../../api/collection'
import { reorderEntry } from '../../api/collection'
import type { NeighborIDs } from '../../lib/reorder'
import { crossesUnknownEdge, moveByOffset, neighborIDs } from '../../lib/reorder'
import { PAGE_SIZE } from '../../lib/listParams'

function SortableRow({
  entry, onMove, atTop, atBottom, pending,
}: {
  entry: Entry
  onMove: (id: string, offset: -1 | 1) => void
  atTop: boolean
  atBottom: boolean
  pending: boolean
}) {
  const { t } = useLingui()
  const { attributes, listeners, setNodeRef, transform, transition } = useSortable({ id: entry.id })
  const displayName = entry.display_name
  return (
    <li
      ref={setNodeRef}
      style={{ transform: CSS.Transform.toString(transform), transition }}
      className="flex items-center gap-3 rounded border border-gray-200 bg-white px-3 py-2"
    >
      <button
        type="button"
        {...attributes}
        {...listeners}
        aria-label={t`Drag ${displayName}`}
        className="cursor-grab text-gray-400"
      >
        {'::'}
      </button>
      <Link to={`/entries/${entry.id}`} className="font-medium hover:underline">
        {entry.display_name}
      </Link>
      <span className="text-xs text-gray-400">{entry.platform?.name ?? '-'}</span>
      <span className="ml-auto flex gap-1">
        <button
          type="button"
          onClick={() => onMove(entry.id, -1)}
          disabled={atTop || pending}
          aria-label={t`Move ${displayName} up`}
          className="rounded border border-gray-300 px-2 text-xs disabled:opacity-30"
        >
          {'^'}
        </button>
        <button
          type="button"
          onClick={() => onMove(entry.id, 1)}
          disabled={atBottom || pending}
          aria-label={t`Move ${displayName} down`}
          className="rounded border border-gray-300 px-2 text-xs disabled:opacity-30"
        >
          {'v'}
        </button>
      </span>
    </li>
  )
}

// BacklogBoard renders the backlog in pure rank order (the query layer
// pins order=asc for rank reads) and turns drops into single reorder
// calls. Optimistic: the cached page reorders immediately; a failure
// refetches. 409 conflicting_order means the list moved somewhere else
// (another tab, another device) - refetch and say so.
//
// entries is always one PAGE_SIZE page of a possibly much larger
// backlog, so a move landing on the visible top/bottom slot cannot in
// general be resolved to a correct after_id/before_id - the true
// neighbor may sit on an unfetched adjacent page. page/totalCount let
// the board tell a page-local edge apart from the true global edge
// (see lib/reorder's crossesUnknownEdge): everywhere else, a move
// stays a page-local, always-safe operation.
export default function BacklogBoard({ entries, page, totalCount }: { entries: Entry[]; page: number; totalCount: number }) {
  const { t } = useLingui()
  const queryClient = useQueryClient()
  const [conflict, setConflict] = useState<string | null>(null)
  const sensors = useSensors(useSensor(PointerSensor))
  const ids = entries.map((e) => e.id)
  const isFirstPage = page === 0
  const isLastPage = page * PAGE_SIZE + entries.length === totalCount

  const reorder = useMutation({
    mutationFn: ({ id, pair }: { id: string; pair: NeighborIDs }) => reorderEntry(id, pair),
    onMutate: async ({ id, pair }) => {
      setConflict(null)
      await queryClient.cancelQueries({ queryKey: ['entries'] })
      const previous = queryClient.getQueriesData<EntryList>({ queryKey: ['entries'] })
      // Optimistically reorder every cached entries page (only the
      // active one is mounted; the rest invalidate below anyway).
      queryClient.setQueriesData<EntryList>({ queryKey: ['entries'] }, (old) => {
        if (!old?.entries) return old
        const current = old.entries
        const from = current.findIndex((e) => e.id === id)
        if (from < 0) return old
        const moved = [...current]
        const [row] = moved.splice(from, 1)
        const anchor = pair.before_id
          ? moved.findIndex((e) => e.id === pair.before_id)
          : moved.length
        moved.splice(anchor < 0 ? moved.length : anchor, 0, row)
        return { ...old, entries: moved }
      })
      return { previous }
    },
    onError: (err, _vars, context) => {
      context?.previous.forEach(([key, data]) => queryClient.setQueryData(key, data))
      setConflict(
        err instanceof ApiError && err.code === 'conflicting_order'
          ? t`The backlog changed somewhere else; the list has been refreshed.`
          : t`The move could not be saved; the list has been refreshed.`,
      )
    },
    onSettled: () => {
      void queryClient.invalidateQueries({ queryKey: ['entries'] })
    },
  })

  const submit = (id: string, pair: NeighborIDs | null) => {
    if (reorder.isPending) return
    if (!pair || crossesUnknownEdge(pair, isFirstPage, isLastPage)) return
    reorder.mutate({ id, pair })
  }
  const handleDragEnd = (event: DragEndEvent) => {
    const overId = event.over?.id
    if (typeof overId !== 'string' || typeof event.active.id !== 'string') return
    submit(event.active.id, neighborIDs(ids, event.active.id, overId))
  }

  return (
    <section aria-label={t`Backlog order`}>
      {conflict && (
        <p role="alert" className="mb-3 rounded bg-amber-50 p-3 text-sm text-amber-800">
          {conflict}
        </p>
      )}
      <DndContext sensors={sensors} collisionDetection={closestCenter} onDragEnd={handleDragEnd}>
        <SortableContext items={ids} strategy={verticalListSortingStrategy}>
          <ul className="flex flex-col gap-2">
            {entries.map((e) => {
              // A row's own Up/Down is disabled whenever that specific
              // move is impossible (moveByOffset null, the page's own
              // bound) or would write a false global edge on this page
              // (see crossesUnknownEdge) - the same guard submit()
              // applies, computed ahead of the click so the button
              // reflects it instead of silently no-opping.
              const up = moveByOffset(ids, e.id, -1)
              const down = moveByOffset(ids, e.id, 1)
              return (
                <SortableRow
                  key={e.id}
                  entry={e}
                  atTop={!up || crossesUnknownEdge(up, isFirstPage, isLastPage)}
                  atBottom={!down || crossesUnknownEdge(down, isFirstPage, isLastPage)}
                  pending={reorder.isPending}
                  onMove={(id, offset) => submit(id, moveByOffset(ids, id, offset))}
                />
              )
            })}
          </ul>
        </SortableContext>
      </DndContext>
    </section>
  )
}
