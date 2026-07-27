import { act, render, screen, within } from '@testing-library/react'
import CopyButton from './CopyButton'

// Mirrors CopyButton's own (unexported) revert window.
const REVERT_MS = 2000

function stubClipboard(writeText: (text: string) => Promise<void>) {
  Object.defineProperty(navigator, 'clipboard', { value: { writeText }, configurable: true })
}

// A click's resulting setState comes off a resolved-mock promise's
// .then(), a microtask - two pumps is the same idiom Explore.test's
// in-flight regression and CommentList's own use to let one settle
// mid-act.
async function click(button: HTMLElement) {
  await act(async () => {
    button.click()
    await Promise.resolve()
    await Promise.resolve()
  })
}

afterEach(() => {
  vi.useRealTimers()
  vi.restoreAllMocks()
})

it('reads Copy link by default', () => {
  stubClipboard(vi.fn().mockResolvedValue(undefined))
  render(<CopyButton text="https://example.test/x" />)
  expect(screen.getByRole('button', { name: 'Copy link' })).toBeInTheDocument()
})

it('reads a custom label at rest', () => {
  stubClipboard(vi.fn().mockResolvedValue(undefined))
  render(<CopyButton text="https://example.test/x" label="Copy profile link" />)
  expect(screen.getByRole('button', { name: 'Copy profile link' })).toBeInTheDocument()
})

it('keeps a stable accessible name across a click while its visible text and a sibling status announce the change', async () => {
  vi.useFakeTimers({ toFake: ['setTimeout', 'clearTimeout'] })
  const writeText = vi.fn().mockResolvedValue(undefined)
  stubClipboard(writeText)
  render(<CopyButton text="https://example.test/shelf" />)
  // Held from before the click and re-asserted after: the accessible
  // name is aria-label, not the swapping text content, so the same
  // query still matches the same element post-click.
  const button = screen.getByRole('button', { name: 'Copy link' })

  await click(button)
  expect(writeText).toHaveBeenCalledWith('https://example.test/shelf')
  expect(screen.getByRole('button', { name: 'Copy link' })).toBe(button)
  expect(button).toHaveTextContent('Copied')
  // The announcement lives in a sibling status region, not nested
  // inside the button - a status nested in a button fights the
  // button's own children-are-presentational semantics.
  expect(within(button).queryByRole('status')).not.toBeInTheDocument()
  expect(screen.getByRole('status')).toHaveTextContent('Copied')

  act(() => {
    vi.advanceTimersByTime(REVERT_MS)
  })
  vi.useRealTimers()
  expect(button).toHaveTextContent('Copy link')
})

it('announces Copy failed through the revert window on a rejected write, with no unhandled rejection', async () => {
  vi.useFakeTimers({ toFake: ['setTimeout', 'clearTimeout'] })
  stubClipboard(vi.fn().mockRejectedValue(new Error('denied')))
  render(<CopyButton text="https://example.test/shelf" />)
  const button = screen.getByRole('button', { name: 'Copy link' })

  // No unhandled rejection reaching here (vitest fails the run on one
  // by default) is itself part of the proof.
  await click(button)
  expect(button).toHaveTextContent('Copy failed')
  expect(screen.getByRole('status')).toHaveTextContent('Copy failed')

  act(() => {
    vi.advanceTimersByTime(REVERT_MS)
  })
  vi.useRealTimers()
  expect(button).toHaveTextContent('Copy link')
})

it('re-clicking mid-window restarts the revert timer instead of letting the stale one fire early', async () => {
  vi.useFakeTimers({ toFake: ['setTimeout', 'clearTimeout'] })
  stubClipboard(vi.fn().mockResolvedValue(undefined))
  render(<CopyButton text="https://example.test/shelf" />)
  const button = screen.getByRole('button', { name: 'Copy link' })

  await click(button)
  expect(button).toHaveTextContent('Copied')

  // Halfway through the first window, click again.
  act(() => {
    vi.advanceTimersByTime(REVERT_MS / 2)
  })
  await click(button)

  // The first window's own expiry has now fully elapsed (1000ms twice
  // over since the first click), but the restarted window only has
  // 1000ms behind it - a stacked, uncleared first timer would have
  // reverted this to the resting label already.
  act(() => {
    vi.advanceTimersByTime(REVERT_MS / 2)
  })
  expect(button).toHaveTextContent('Copied')

  act(() => {
    vi.advanceTimersByTime(REVERT_MS / 2)
  })
  vi.useRealTimers()
  expect(button).toHaveTextContent('Copy link')
})

it("a later overlapping click's settle clears an earlier still-pending click's timer instead of leaving it to fire on its own", async () => {
  // Reproduces the clobber: click A, then click B before A's write
  // has resolved - copy()'s own clearTimeout has nothing pending to
  // clear yet at either click, so this depends entirely on settle()
  // itself clearing a timer set by an earlier, still-in-flight click.
  vi.useFakeTimers({ toFake: ['setTimeout', 'clearTimeout'] })
  let resolveA: () => void = () => {}
  let resolveB: () => void = () => {}
  const writeText = vi
    .fn()
    .mockReturnValueOnce(new Promise<void>((resolve) => (resolveA = resolve)))
    .mockReturnValueOnce(new Promise<void>((resolve) => (resolveB = resolve)))
  stubClipboard(writeText)
  render(<CopyButton text="https://example.test/shelf" />)
  const button = screen.getByRole('button', { name: 'Copy link' })

  act(() => {
    button.click()
  })
  act(() => {
    button.click()
  })
  expect(writeText).toHaveBeenCalledTimes(2)

  // A resolves first: its settle() starts a revert timer 2000ms out
  // from right now.
  await act(async () => {
    resolveA()
    await Promise.resolve()
    await Promise.resolve()
  })

  // 500ms later, B resolves too: its settle() must clear A's pending
  // timer, not just overwrite the ref on top of it, before starting
  // its own 2000ms-out timer.
  act(() => {
    vi.advanceTimersByTime(500)
  })
  await act(async () => {
    resolveB()
    await Promise.resolve()
    await Promise.resolve()
  })

  // 1600ms further on: 2100ms since A settled (100ms past A's own
  // revert point) but only 1600ms since B settled (still inside B's
  // window). An uncleared A timer would fire here and revert the
  // label early, out from under B.
  act(() => {
    vi.advanceTimersByTime(1600)
  })
  expect(button).toHaveTextContent('Copied')
  expect(screen.getByRole('status')).toHaveTextContent('Copied')

  // Past B's own 2000ms window, it reverts for real.
  act(() => {
    vi.advanceTimersByTime(500)
  })
  vi.useRealTimers()
  expect(button).toHaveTextContent('Copy link')
})

it('unmounting mid-window clears the pending timer, firing no update and no act warning', async () => {
  vi.useFakeTimers({ toFake: ['setTimeout', 'clearTimeout'] })
  stubClipboard(vi.fn().mockResolvedValue(undefined))
  const { unmount } = render(<CopyButton text="https://example.test/shelf" />)
  const button = screen.getByRole('button', { name: 'Copy link' })

  await click(button)
  expect(button).toHaveTextContent('Copied')

  const errorSpy = vi.spyOn(console, 'error')
  act(() => unmount())
  act(() => {
    vi.advanceTimersByTime(REVERT_MS)
  })
  vi.useRealTimers()
  expect(errorSpy).not.toHaveBeenCalled()
})

it('unmounting while a click\'s clipboard write is still pending drops the result instead of leaking a revert timer', async () => {
  vi.useFakeTimers({ toFake: ['setTimeout', 'clearTimeout'] })
  let resolveWrite: () => void = () => {}
  const writeText = vi.fn(() => new Promise<void>((resolve) => (resolveWrite = resolve)))
  stubClipboard(writeText)
  const { unmount } = render(<CopyButton text="https://example.test/shelf" />)
  const button = screen.getByRole('button', { name: 'Copy link' })

  act(() => {
    button.click()
  })
  expect(writeText).toHaveBeenCalledTimes(1)
  expect(vi.getTimerCount()).toBe(0)

  const errorSpy = vi.spyOn(console, 'error')
  act(() => unmount())

  // The write resolves only after unmount: settle() must see the
  // mounted flag is false and bail out before touching state or
  // scheduling a new revert timer - otherwise that timer leaks, since
  // nothing is left to ever clear it.
  await act(async () => {
    resolveWrite()
    await Promise.resolve()
    await Promise.resolve()
  })
  expect(vi.getTimerCount()).toBe(0)
  expect(errorSpy).not.toHaveBeenCalled()
  vi.useRealTimers()
})
