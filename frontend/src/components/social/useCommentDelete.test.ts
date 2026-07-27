import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { act, renderHook } from '@testing-library/react'
import { createElement, type ReactNode } from 'react'
import { UNDO_WINDOW_MS, useCommentDelete } from './useCommentDelete'

// Kept as a plain .ts file (the hook itself has no JSX either); the
// provider wrapper below uses createElement instead of JSX since a
// .ts file cannot parse JSX syntax.
function setup(shelfId = 's1') {
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } })
  const invalidateSpy = vi.spyOn(qc, 'invalidateQueries')
  const wrapper = ({ children }: { children: ReactNode }) =>
    createElement(QueryClientProvider, { client: qc }, children)
  const hook = renderHook(() => useCommentDelete(shelfId), { wrapper })
  return { hook, invalidateSpy }
}

// Every test gives fetch a resolvable Promise, even ones that never
// expect it to be called: the hook's own unmount-cleanup flushes any
// still-pending delete (a real setTimeout the test never advanced),
// and an un-mocked bare vi.fn() would return undefined, breaking
// .finally() mid-teardown.
function stubFetch() {
  const fetchMock = vi.fn().mockResolvedValue(new Response(null, { status: 204 }))
  vi.stubGlobal('fetch', fetchMock)
  return fetchMock
}

afterEach(() => {
  vi.useRealTimers()
  vi.unstubAllGlobals()
})

// Each test unmounts explicitly, inside its own body, rather than
// relying on RTL's implicit auto-cleanup: that cleanup registers
// lazily on the first render call, which lands AFTER this file's own
// afterEach in the hook chain - by the time it fires, fetch is
// already unstubbed, and the hook's unmount-triggered flush (any
// still-pending delete commits on unmount, same as a pagehide) would
// hit the real fetch and reject on a relative URL. Unmounting here
// keeps the flush inside the still-mocked window.
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

// Regression for the stale-timer clear guard at the top of
// requestDelete: reachable twice in a row for the same id (a second
// Delete click before the first commits), and without the guard both
// timers would independently fire commit(id) at expiry, sending two
// DELETEs for one comment.
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
  expect(fetchMock).toHaveBeenCalledWith('/api/comments/c1', { method: 'DELETE', keepalive: false })
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

  // Already expired: requestDelete's own timer fires and fully
  // settles (commit() deletes the timers entry, then the resolved
  // fetch clears the id from pendingIds) - undo afterward must still
  // be a no-op, not a second DELETE.
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
  expect(fetchMock).toHaveBeenCalledWith('/api/comments/c1', { method: 'DELETE', keepalive: false })
  expect(invalidateSpy).toHaveBeenCalledWith({ queryKey: ['shelfComments', 's1'] })
  expect(invalidateSpy).toHaveBeenCalledWith({ queryKey: ['shelfSummary', 's1'] })
  expect(hook.result.current.pendingIds.has('c1')).toBe(false)
  teardown(hook)
})

// Regression for a real bug: undo() used to clear pendingIds
// unconditionally, even when the timer it looked up was already gone
// because commit() had fired at expiry. That "restored" the row in
// the UI (Undo button disappears, comment looks back to normal)
// while its DELETE was still in flight - a false success. The fetch
// here deliberately never resolves during the in-flight window so the
// test can observe pendingIds at that exact moment.
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
  // The timer fired and commit() kicked off the DELETE, but the fetch
  // above is still unresolved: the id must still read as pending.
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
  // No unhandled rejection reaches here (vitest fails the run on one
  // by default) - the test completing at all is part of the proof.
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
  // c1 has now waited 3000ms of its own 7000ms window; this advance
  // covers the remaining 4000ms - c1 expires while c2 (requested at
  // the 3000ms mark) has only waited 4000ms of its own window.
  await act(async () => {
    await vi.advanceTimersByTimeAsync(UNDO_WINDOW_MS - 3000)
  })
  expect(fetchMock).toHaveBeenCalledTimes(1)
  expect(fetchMock).toHaveBeenCalledWith('/api/comments/c1', { method: 'DELETE', keepalive: false })
  expect(hook.result.current.pendingIds.has('c1')).toBe(false)
  expect(hook.result.current.pendingIds.has('c2')).toBe(true)
  await act(async () => {
    await vi.advanceTimersByTimeAsync(3000)
  })
  expect(fetchMock).toHaveBeenCalledTimes(2)
  expect(fetchMock).toHaveBeenCalledWith('/api/comments/c2', { method: 'DELETE', keepalive: false })
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
  expect(fetchMock).toHaveBeenCalledWith('/api/comments/c1', { method: 'DELETE', keepalive: true })
  expect(fetchMock).toHaveBeenCalledWith('/api/comments/c2', { method: 'DELETE', keepalive: true })
  // timers cleared: letting the original window elapse fires nothing new
  await act(async () => {
    await vi.advanceTimersByTimeAsync(UNDO_WINDOW_MS)
  })
  expect(fetchMock).toHaveBeenCalledTimes(2)
  teardown(hook)
})

// Dedicated coverage for the unmount-flush path itself: every other
// test's teardown() exercises it incidentally (an unmounted still-
// pending id flushes), but nothing asserts the call it makes - so
// deleting the flush() call in the hook's cleanup would pass every
// other test in this file unnoticed.
it('unmount before expiry flushes the pending delete via a keepalive commit exactly once', async () => {
  vi.useFakeTimers({ toFake: ['setTimeout', 'clearTimeout'] })
  const fetchMock = stubFetch()
  const { hook } = setup('s1')
  act(() => hook.result.current.requestDelete('c1'))
  expect(fetchMock).not.toHaveBeenCalled()

  act(() => hook.unmount())
  expect(fetchMock).toHaveBeenCalledTimes(1)
  expect(fetchMock).toHaveBeenCalledWith('/api/comments/c1', { method: 'DELETE', keepalive: true })

  // The original expiry timer must not also fire: that would double-commit.
  await act(async () => {
    await vi.advanceTimersByTimeAsync(UNDO_WINDOW_MS)
  })
  expect(fetchMock).toHaveBeenCalledTimes(1)
  // No teardown(hook) here - unmount is the very thing under test above.
})
