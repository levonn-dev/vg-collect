import { useCallback, useEffect, useRef, useState } from 'react'
import { useQueryClient } from '@tanstack/react-query'
import { sendJSON } from '../../api/client'

export const UNDO_WINDOW_MS = 7000

// Client-side undo window for SELF comment deletion: the DELETE is
// deferred, not the tombstone - the server never learns about undone
// deletes. Owner-removal must NOT use this hook (moderation commits
// immediately). pagehide flushes pending deletes with keepalive so a
// closed tab still commits; a missed flush fails benign (the comment
// survives).
export function useCommentDelete(shelfId: string) {
  const qc = useQueryClient()
  const [pendingIds, setPendingIds] = useState<Set<string>>(new Set())
  const timers = useRef(new Map<string, ReturnType<typeof setTimeout>>())

  // A failed DELETE (network or server) must not vanish the comment
  // locally forever: .catch() swallows the rejection (no unhandled
  // rejection noise) so .finally() always runs next, invalidating
  // both queries on success AND failure - a failed delete refetches
  // server truth and the comment reappears instead of silently
  // staying gone.
  const commit = useCallback((id: string, keepalive = false) => {
    timers.current.delete(id)
    void sendJSON<void>('DELETE', `/api/comments/${id}`, undefined, { keepalive })
      .catch(() => {})
      .finally(() => {
        setPendingIds(prev => { const next = new Set(prev); next.delete(id); return next })
        void qc.invalidateQueries({ queryKey: ['shelfComments', shelfId] })
        void qc.invalidateQueries({ queryKey: ['shelfSummary', shelfId] })
      })
  }, [qc, shelfId])

  const requestDelete = useCallback((id: string) => {
    // Clear any pre-existing timer for this id first: reachable twice
    // in a row (e.g. a second Delete click before the first commits)
    // would otherwise leak the old timer and double-fire commit(id).
    const existing = timers.current.get(id)
    if (existing) clearTimeout(existing)
    setPendingIds(prev => new Set(prev).add(id))
    timers.current.set(id, setTimeout(() => commit(id), UNDO_WINDOW_MS))
  }, [commit])

  // Look up the timer FIRST and bail if it is gone (already committed,
  // already undone, or never requested): commit() deletes the timers
  // entry and fires the DELETE synchronously at expiry, but pendingIds
  // only clears once that fetch settles. Touching pendingIds here
  // without this guard would "restore" a comment whose DELETE is
  // still in flight, out from under the request that owns it.
  const undo = useCallback((id: string) => {
    const t = timers.current.get(id)
    if (!t) return
    clearTimeout(t)
    timers.current.delete(id)
    setPendingIds(prev => { const next = new Set(prev); next.delete(id); return next })
  }, [])

  useEffect(() => {
    const flush = () => {
      for (const [id, t] of timers.current) {
        clearTimeout(t)
        commit(id, true)
      }
    }
    window.addEventListener('pagehide', flush)
    return () => {
      window.removeEventListener('pagehide', flush)
      flush() // unmount commits too - navigating away is a pagehide-equivalent
    }
  }, [commit])

  return { pendingIds, requestDelete, undo }
}
