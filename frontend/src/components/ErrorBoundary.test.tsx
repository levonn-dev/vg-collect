import { render, screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { recordUncaughtError } from '../telemetry'
import ErrorBoundary from './ErrorBoundary'

// Isolates from initTelemetry's no-op-before-init state.
vi.mock('../telemetry', async (importOriginal) => {
  const mod = await importOriginal<typeof import('../telemetry')>()
  return { ...mod, recordUncaughtError: vi.fn() }
})

function Thrower(): never {
  throw new Error('kaboom')
}

// React logs every boundary-caught error via console.error (default
// onCaughtError); silenced file-wide rather than asserted on.
beforeEach(() => {
  vi.spyOn(console, 'error').mockImplementation(() => {})
})

afterEach(() => {
  vi.restoreAllMocks()
})

// No renderWithI18n: the fallback must work even if the i18n runtime crashed,
// so tests run under plain RTL render() with zero providers.

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
