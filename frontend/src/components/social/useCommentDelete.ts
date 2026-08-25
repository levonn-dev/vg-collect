import { useCallback, useEffect, useRef, useState } from 'react'
import { useQueryClient } from '@tanstack/react-query'
import { deleteComment } from '../../api/social'
import { invalidateShelfSocial } from '../../lib/shelfQueries'

export const UNDO_WINDOW_MS = 7000

// DELETE is deferred, not the tombstone: the server never learns about
// undone deletes. Owner-removal must NOT use this (moderation commits
// immediately). pagehide flushes with keepalive; a missed flush is benign.
export function useCommentDelete(shelfId: string) {
  const qc = useQueryClient()
  const [pendingIds, setPendingIds] = useState<Set<string>>(new Set())
  const timers = useRef(new Map<string, ReturnType<typeof setTimeout>>())

  // .catch() swallows the rejection (no unhandled rejection noise) so
  // .finally() always invalidates, on success and failure alike - a failed
  // delete refetches server truth and the comment reappears.
  const commit = useCallback((id: string, keepalive = false) => {
    timers.current.delete(id)
    void deleteComment(id, { keepalive })
      .catch(() => {})
      .finally(() => {
        setPendingIds(prev => { const next = new Set(prev); next.delete(id); return next })
        invalidateShelfSocial(qc, shelfId)
      })
  }, [qc, shelfId])

  const requestDelete = useCallback((id: string) => {
    // Clears any pre-existing timer first: a second Delete click before the
    // first commits would otherwise leak the old timer and double-fire commit.
    const existing = timers.current.get(id)
    if (existing) clearTimeout(existing)
    setPendingIds(prev => new Set(prev).add(id))
    timers.current.set(id, setTimeout(() => commit(id), UNDO_WINDOW_MS))
  }, [commit])

  // Bails if the timer is gone (already committed/undone/never requested):
  // commit() clears the timer synchronously, but pendingIds only clears once
  // the fetch settles - touching it here would restore an in-flight delete.
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
