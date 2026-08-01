import { render, screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { recordUncaughtError } from '../telemetry'
import ErrorBoundary from './ErrorBoundary'

// Isolates the component from initTelemetry's no-op-before-init state,
// same seam ProsePage.test.tsx and locale.test.ts use.
vi.mock('../telemetry', async (importOriginal) => {
  const mod = await importOriginal<typeof import('../telemetry')>()
  return { ...mod, recordUncaughtError: vi.fn() }
})

function Thrower(): never {
  throw new Error('kaboom')
}

// componentDidCatch running is the whole point of the tests below, and
// React logs every boundary-caught error via console.error (its
// default onCaughtError root option) - silenced file-wide, per RTL's
// own convention for testing error boundaries, rather than asserted on.
beforeEach(() => {
  vi.spyOn(console, 'error').mockImplementation(() => {})
})

afterEach(() => {
  vi.restoreAllMocks()
})

// Deliberate: nothing in this file reaches for renderWithI18n. The
// fallback must work even if the i18n runtime is what crashed, so the
// proof is every test here - crash included - passing under the plain
// RTL render(), with zero providers in the tree.

it('passes children through unchanged, with no wrapper element, when nothing throws', () => {
  const { container } = render(
    <ErrorBoundary>
      <p>All good</p>
    </ErrorBoundary>,
  )
  expect(container.innerHTML).toBe('<p>All good</p>')
  expect(recordUncaughtError).not.toHaveBeenCalled()
})

it('shows the fallback and records one boundary telemetry event when a child throws', () => {
  render(
    <ErrorBoundary>
      <Thrower />
    </ErrorBoundary>,
  )
  expect(screen.getByRole('heading', { name: 'Something went wrong' })).toBeInTheDocument()
  expect(screen.getByRole('alert')).toHaveTextContent(
    'The page hit an error it could not recover from. Reloading usually fixes it.',
  )
  expect(screen.getByRole('button', { name: 'Reload' })).toBeInTheDocument()
  expect(recordUncaughtError).toHaveBeenCalledTimes(1)
  expect(recordUncaughtError).toHaveBeenCalledWith('boundary')
})

describe('reload button', () => {
  const originalLocation = window.location

  afterEach(() => {
    Object.defineProperty(window, 'location', { configurable: true, value: originalLocation })
  })

  it('reloads the page when clicked', async () => {
    const reload = vi.fn()
    Object.defineProperty(window, 'location', { configurable: true, value: { reload } })
    render(
      <ErrorBoundary>
        <Thrower />
      </ErrorBoundary>,
    )
    await userEvent.click(screen.getByRole('button', { name: 'Reload' }))
    expect(reload).toHaveBeenCalledTimes(1)
  })
})
