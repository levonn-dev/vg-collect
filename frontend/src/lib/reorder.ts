export interface NeighborIDs {
  after_id: string | null
  before_id: string | null
}

// Simulates the array move, reads the moved item's new neighbors. Null
// = no-op/stale ids. Caller guarantees rank-ascending order, so visual
// neighbors are rank neighbors.
export function neighborIDs(ids: string[], activeId: string, overId: string): NeighborIDs | null {
  const from = ids.indexOf(activeId)
  const to = ids.indexOf(overId)
  if (from < 0 || to < 0 || from === to) return null
  const moved = [...ids]
  moved.splice(from, 1)
  moved.splice(to, 0, activeId)
  return { after_id: moved[to - 1] ?? null, before_id: moved[to + 1] ?? null }
}

// moveByOffset is the keyboard path: one slot up or down.
export function moveByOffset(ids: string[], activeId: string, offset: -1 | 1): NeighborIDs | null {
  const from = ids.indexOf(activeId)
  const to = from + offset
  if (from < 0 || to < 0 || to >= ids.length) return null
  return neighborIDs(ids, activeId, ids[to])
}

// A null neighbor id only means the true backlog edge when
// isFirstPage/isLastPage says so; otherwise it's the page's own visible
// boundary, whose real neighbor sits on an unfetched page.
export function crossesUnknownEdge(pair: NeighborIDs, isFirstPage: boolean, isLastPage: boolean): boolean {
  return (pair.after_id === null && !isFirstPage) || (pair.before_id === null && !isLastPage)
}
