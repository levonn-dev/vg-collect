export interface NeighborIDs {
  after_id: string | null
  before_id: string | null
}

// neighborIDs maps a drag (active dropped onto over's slot) onto the
// reorder contract's neighbor pair: simulate the array move, then read
// the moved item's new neighbors. Null means "nothing to do" (no-op
// drop or stale ids). The caller guarantees ids arrive in rank order
// ascending - the same order the board renders - so visual neighbors
// ARE rank neighbors and the server's straddle check agrees.
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
