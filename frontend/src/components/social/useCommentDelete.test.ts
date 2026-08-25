import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { act, renderHook } from '@testing-library/react'
import { createElement, type ReactNode } from 'react'
import { UNDO_WINDOW_MS, useCommentDelete } from './useCommentDelete'
import { calledPath, requestPath } from '../../test/fixtures'

// Plain .ts file (hook has no JSX either); wrapper uses createElement since
// .ts can't parse JSX.
function setup(shelfId = 's1') {
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } })
  const invalidateSpy = vi.spyOn(qc, 'invalidateQueries')
  const wrapper = ({ children }: { children: ReactNode }) =>
    createElement(QueryClientProvider, { client: qc }, children)
  const hook = renderHook(() => useCommentDelete(shelfId), { wrapper })
  return { hook, invalidateSpy }
}

// Every test needs a resolvable fetch, even ones that never expect a call:
// unmount-cleanup flushes any still-pending delete, and a bare vi.fn() would
// return undefined, breaking .finally() mid-teardown.
function stubFetch() {
  const fetchMock = vi.fn().mockResolvedValue(new Response(null, { status: 204 }))
  vi.stubGlobal('fetch', fetchMock)
  return fetchMock
}

afterEach(() => {
  vi.useRealTimers()
  vi.unstubAllGlobals()
})

// Explicit unmount, not RTL's auto-cleanup: that registers lazily on first
// render, landing after this file's own afterEach unstubs fetch, so the
// unmount-triggered flush would hit the real fetch and reject. Unmounting
// here keeps the flush inside the still-mocked window.
function teardown(hook: ReturnType<typeof setup>['hook']) {
  act(() => hook.unmount())
}

it('requestDelete adds the id to pendingIds and fires no fetch', () => {
  const fetchMock = stubFetch()
  const { hook } = setup()
  act(() => hook.result.current.requestDelete('c1'))
  expect(hook.result.current.pendingIds.has('c1')).toBe(true)
  expect(fetchMock).not.toHaveBeenCalled()
  teardown(hook)
})

// Two Delete clicks for the same id before the first commits: without the
// stale-timer clear guard, both timers would independently fire commit(id),
// sending two DELETEs.
it('two requestDelete calls for the same id clear the stale timer, firing exactly one DELETE at expiry', async () => {
  vi.useFakeTimers({ toFake: ['setTimeout', 'clearTimeout'] })
  const fetchMock = stubFetch()
  const { hook } = setup('s1')
  act(() => hook.result.current.requestDelete('c1'))
  act(() => hook.result.current.requestDelete('c1'))
  await act(async () => {
    await vi.advanceTimersByTimeAsync(UNDO_WINDOW_MS)
  })
  expect(fetchMock).toHaveBeenCalledTimes(1)
  expect(calledPath(fetchMock, 0)).toBe('/api/comments/c1')
  const req = fetchMock.mock.calls[0][0] as Request
  expect(req.method).toBe('DELETE')
  expect(req.keepalive).toBe(false)
  expect(hook.result.current.pendingIds.has('c1')).toBe(false)
  teardown(hook)
})

it('undo before expiry removes the id and no fetch ever fires', async () => {
  vi.useFakeTimers({ toFake: ['setTimeout', 'clearTimeout'] })
  const fetchMock = stubFetch()
  const { hook } = setup()
  act(() => hook.result.current.requestDelete('c1'))
  act(() => hook.result.current.undo('c1'))
  expect(hook.result.current.pendingIds.has('c1')).toBe(false)
  await act(async () => {
    await vi.advanceTimersByTimeAsync(UNDO_WINDOW_MS)
  })
  expect(fetchMock).not.toHaveBeenCalled()
  teardown(hook)
})

it('undo on an id with no pending timer (already expired, or never requested) is a harmless no-op', async () => {
  vi.useFakeTimers({ toFake: ['setTimeout', 'clearTimeout'] })
  const fetchMock = stubFetch()
  const { hook } = setup()

  // Never requested: no timer ever existed for this id.
  act(() => hook.result.current.undo('never-requested'))
  expect(hook.result.current.pendingIds.has('never-requested')).toBe(false)
  expect(fetchMock).not.toHaveBeenCalled()

  // Already expired: the timer fires and fully settles (commit() clears the
  // entry, fetch clears pendingIds); undo afterward must still be a no-op.
  act(() => hook.result.current.requestDelete('c1'))
  await act(async () => {
    await vi.advanceTimersByTimeAsync(UNDO_WINDOW_MS)
  })
  expect(fetchMock).toHaveBeenCalledTimes(1)
  fetchMock.mockClear()

  act(() => hook.result.current.undo('c1'))
  expect(hook.result.current.pendingIds.has('c1')).toBe(false)
  expect(fetchMock).not.toHaveBeenCalled()

  teardown(hook)
})

it('fires exactly one DELETE at expiry, invalidates the comment queries, and clears the id', async () => {
  vi.useFakeTimers({ toFake: ['setTimeout', 'clearTimeout'] })
  const fetchMock = stubFetch()
  const { hook, invalidateSpy } = setup('s1')
  act(() => hook.result.current.requestDelete('c1'))
  await act(async () => {
    await vi.advanceTimersByTimeAsync(UNDO_WINDOW_MS)
  })
  expect(fetchMock).toHaveBeenCalledTimes(1)
  expect(calledPath(fetchMock, 0)).toBe('/api/comments/c1')
  const req = fetchMock.mock.calls[0][0] as Request
  expect(req.method).toBe('DELETE')
  expect(req.keepalive).toBe(false)
  expect(invalidateSpy).toHaveBeenCalledWith({ queryKey: ['shelfComments', 's1'] })
  expect(invalidateSpy).toHaveBeenCalledWith({ queryKey: ['shelfSummary', 's1'] })
  expect(hook.result.current.pendingIds.has('c1')).toBe(false)
  teardown(hook)
})

// undo() unconditionally clearing pendingIds when the timer is already gone
// would falsely restore the row (Undo disappears) while its DELETE is still
// in flight. Fetch never resolves here so the test can observe that moment.
it('undo while the expiry-fired commit is still in flight does not falsely restore the row', async () => {
  vi.useFakeTimers({ toFake: ['setTimeout', 'clearTimeout'] })
  let resolveFetch!: (res: Response) => void
  const fetchMock = vi.fn().mockImplementation(
    () => new Promise<Response>((resolve) => { resolveFetch = resolve }),
  )
  vi.stubGlobal('fetch', fetchMock)
  const { hook } = setup('s1')
  act(() => hook.result.current.requestDelete('c1'))
  await act(async () => {
    await vi.advanceTimersByTimeAsync(UNDO_WINDOW_MS)
  })
  // Timer fired and commit() started the DELETE, but the fetch is still
  // unresolved: the id must still read as pending.
  expect(fetchMock).toHaveBeenCalledTimes(1)
  expect(hook.result.current.pendingIds.has('c1')).toBe(true)

  act(() => hook.result.current.undo('c1'))
  expect(hook.result.current.pendingIds.has('c1')).toBe(true)
  expect(fetchMock).toHaveBeenCalledTimes(1)

  // Settling the in-flight DELETE completes the commit normally.
  await act(async () => {
    resolveFetch(new Response(null, { status: 204 }))
    await Promise.resolve()
    await Promise.resolve()
  })
  expect(hook.result.current.pendingIds.has('c1')).toBe(false)
  teardown(hook)
})

it('a failed commit swallows the rejection and still invalidates so the comment reappears', async () => {
  vi.useFakeTimers({ toFake: ['setTimeout', 'clearTimeout'] })
  const fetchMock = vi.fn().mockRejectedValue(new Error('network down'))
  vi.stubGlobal('fetch', fetchMock)
  const { hook, invalidateSpy } = setup('s1')
  act(() => hook.result.current.requestDelete('c1'))
  // No unhandled rejection reaching here (vitest fails on one) is itself part of the proof.
  await act(async () => {
    await vi.advanceTimersByTimeAsync(UNDO_WINDOW_MS)
  })
  expect(invalidateSpy).toHaveBeenCalledWith({ queryKey: ['shelfComments', 's1'] })
  expect(invalidateSpy).toHaveBeenCalledWith({ queryKey: ['shelfSummary', 's1'] })
  expect(hook.result.current.pendingIds.has('c1')).toBe(false)
  teardown(hook)
})

it('two pending deletes expire independently', async () => {
  vi.useFakeTimers({ toFake: ['setTimeout', 'clearTimeout'] })
  const fetchMock = stubFetch()
  const { hook } = setup()
  act(() => hook.result.current.requestDelete('c1'))
  await act(async () => {
    await vi.advanceTimersByTimeAsync(3000)
  })
  act(() => hook.result.current.requestDelete('c2'))
  // c1 has waited 3000ms of its 7000ms window; this advance covers the
  // remaining 4000ms, expiring c1 while c2 (requested at 3000ms) has only waited 4000ms.
  await act(async () => {
    await vi.advanceTimersByTimeAsync(UNDO_WINDOW_MS - 3000)
  })
  expect(fetchMock).toHaveBeenCalledTimes(1)
  expect(calledPath(fetchMock, 0)).toBe('/api/comments/c1')
  expect((fetchMock.mock.calls[0][0] as Request).keepalive).toBe(false)
  expect(hook.result.current.pendingIds.has('c1')).toBe(false)
  expect(hook.result.current.pendingIds.has('c2')).toBe(true)
  await act(async () => {
    await vi.advanceTimersByTimeAsync(3000)
  })
  expect(fetchMock).toHaveBeenCalledTimes(2)
  expect(calledPath(fetchMock, 1)).toBe('/api/comments/c2')
  expect((fetchMock.mock.calls[1][0] as Request).keepalive).toBe(false)
  expect(hook.result.current.pendingIds.has('c2')).toBe(false)
  teardown(hook)
})

it('pagehide flushes every pending id immediately via a keepalive fetch and clears their timers', async () => {
  vi.useFakeTimers({ toFake: ['setTimeout', 'clearTimeout'] })
  const fetchMock = stubFetch()
  const { hook } = setup()
  act(() => hook.result.current.requestDelete('c1'))
  act(() => hook.result.current.requestDelete('c2'))
  await act(async () => {
    window.dispatchEvent(new Event('pagehide'))
    await Promise.resolve()
  })
  expect(fetchMock).toHaveBeenCalledTimes(2)
  const flushed = fetchMock.mock.calls.map((c) => {
    const req = c[0] as Request
    return { path: requestPath(req), method: req.method, keepalive: req.keepalive }
  })
  expect(flushed).toContainEqual({ path: '/api/comments/c1', method: 'DELETE', keepalive: true })
  expect(flushed).toContainEqual({ path: '/api/comments/c2', method: 'DELETE', keepalive: true })
  // timers cleared: letting the original window elapse fires nothing new
  await act(async () => {
    await vi.advanceTimersByTimeAsync(UNDO_WINDOW_MS)
  })
  expect(fetchMock).toHaveBeenCalledTimes(2)
  teardown(hook)
})

// Every other test's teardown() exercises the unmount-flush incidentally but
// asserts nothing about it, so deleting the hook's flush() call would pass
// unnoticed elsewhere.
it('unmount before expiry flushes the pending delete via a keepalive commit exactly once', async () => {
  vi.useFakeTimers({ toFake: ['setTimeout', 'clearTimeout'] })
  const fetchMock = stubFetch()
  const { hook } = setup('s1')
  act(() => hook.result.current.requestDelete('c1'))
  expect(fetchMock).not.toHaveBeenCalled()

  act(() => hook.unmount())
  expect(fetchMock).toHaveBeenCalledTimes(1)
  expect(calledPath(fetchMock, 0)).toBe('/api/comments/c1')
  const req = fetchMock.mock.calls[0][0] as Request
  expect(req.method).toBe('DELETE')
  expect(req.keepalive).toBe(true)

  // The original expiry timer must not also fire: that would double-commit.
  await act(async () => {
    await vi.advanceTimersByTimeAsync(UNDO_WINDOW_MS)
  })
  expect(fetchMock).toHaveBeenCalledTimes(1)
  // No teardown(hook) here - unmount is the very thing under test above.
})
