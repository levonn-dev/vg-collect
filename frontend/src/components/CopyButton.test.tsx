import { act, screen, within } from '@testing-library/react'
import { renderWithI18n } from '../test/i18n'
import CopyButton from './CopyButton'

// Mirrors CopyButton's own (unexported) revert window.
const REVERT_MS = 2000

function stubClipboard(writeText: (text: string) => Promise<void>) {
  Object.defineProperty(navigator, 'clipboard', { value: { writeText }, configurable: true })
}

// Two microtask pumps: setState resolves via the mocked promise's .then().
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
  renderWithI18n(<CopyButton text="https://example.test/x" />)
  expect(screen.getByRole('button', { name: 'Copy link' })).toBeInTheDocument()
})

it('reads a custom label at rest', () => {
  stubClipboard(vi.fn().mockResolvedValue(undefined))
  renderWithI18n(<CopyButton text="https://example.test/x" label="Copy profile link" />)
  expect(screen.getByRole('button', { name: 'Copy profile link' })).toBeInTheDocument()
})

it('keeps a stable accessible name across a click while its visible text and a sibling status announce the change', async () => {
  vi.useFakeTimers({ toFake: ['setTimeout', 'clearTimeout'] })
  const writeText = vi.fn().mockResolvedValue(undefined)
  stubClipboard(writeText)
  renderWithI18n(<CopyButton text="https://example.test/shelf" />)
  // aria-label doesn't swap with text, so the same query matches post-click.
  const button = screen.getByRole('button', { name: 'Copy link' })

  await click(button)
  expect(writeText).toHaveBeenCalledWith('https://example.test/shelf')
  expect(screen.getByRole('button', { name: 'Copy link' })).toBe(button)
  expect(button).toHaveTextContent('Copied')
  // Sibling status region: nested in the button it would fight
  // presentational-children semantics.
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
  renderWithI18n(<CopyButton text="https://example.test/shelf" />)
  const button = screen.getByRole('button', { name: 'Copy link' })

  // No unhandled rejection reaching here is itself part of the proof.
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
  renderWithI18n(<CopyButton text="https://example.test/shelf" />)
  const button = screen.getByRole('button', { name: 'Copy link' })

  await click(button)
  expect(button).toHaveTextContent('Copied')

  // Halfway through the first window, click again.
  act(() => {
    vi.advanceTimersByTime(REVERT_MS / 2)
  })
  await click(button)

  // 2000ms has elapsed since the first click (its own timer would have fired),
  // but the restarted timer is only 1000ms in.
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
  // Click B before A resolves: copy()'s clearTimeout has nothing pending yet,
  // so only settle() clearing A's earlier timer prevents the clobber.
  vi.useFakeTimers({ toFake: ['setTimeout', 'clearTimeout'] })
  let resolveA: () => void = () => {}
  let resolveB: () => void = () => {}
  const writeText = vi
    .fn()
    .mockReturnValueOnce(new Promise<void>((resolve) => (resolveA = resolve)))
    .mockReturnValueOnce(new Promise<void>((resolve) => (resolveB = resolve)))
  stubClipboard(writeText)
  renderWithI18n(<CopyButton text="https://example.test/shelf" />)
  const button = screen.getByRole('button', { name: 'Copy link' })

  act(() => {
    button.click()
  })
  act(() => {
    button.click()
  })
  expect(writeText).toHaveBeenCalledTimes(2)

  // A resolves first; its settle() starts a 2000ms revert timer.
  await act(async () => {
    resolveA()
    await Promise.resolve()
    await Promise.resolve()
  })

  // 500ms later B resolves; its settle() must clear A's pending timer before
  // starting its own.
  act(() => {
    vi.advanceTimersByTime(500)
  })
  await act(async () => {
    resolveB()
    await Promise.resolve()
    await Promise.resolve()
  })

  // 2100ms since A settled (past A's revert point) but only 1600ms since B;
  // an uncleared A timer would revert the label early, out from under B.
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
  const { unmount } = renderWithI18n(<CopyButton text="https://example.test/shelf" />)
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
  const { unmount } = renderWithI18n(<CopyButton text="https://example.test/shelf" />)
  const button = screen.getByRole('button', { name: 'Copy link' })

  act(() => {
    button.click()
  })
  expect(writeText).toHaveBeenCalledTimes(1)
  expect(vi.getTimerCount()).toBe(0)

  const errorSpy = vi.spyOn(console, 'error')
  act(() => unmount())

  // Write resolves after unmount; settle() must see mounted=false and bail
  // before scheduling a timer nothing would ever clear.
  await act(async () => {
    resolveWrite()
    await Promise.resolve()
    await Promise.resolve()
  })
  expect(vi.getTimerCount()).toBe(0)
  expect(errorSpy).not.toHaveBeenCalled()
  vi.useRealTimers()
})
