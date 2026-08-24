import { render, screen } from '@testing-library/react'
import { renderWithI18n } from '../test/i18n'
import { refetchWarning, renderQueryState } from './queryBoundary'

// The three states a query can be in from this helper's point of view:
// pending (no data yet), a first-load error (isError, no prior data -
// nothing to fall back on), and a refetch error (isError, but data
// from a prior successful fetch is still there). A clean success
// shares the refetch error's "let the caller's real content through"
// outcome, so it rides along with that group below rather than getting
// a fourth bucket of its own.
const pending = { isPending: true, isError: false, data: undefined }
const loadError = { isPending: false, isError: true, data: undefined }
const refetchError = { isPending: false, isError: true, data: 'stale data' }
const success = { isPending: false, isError: false, data: 'fresh data' }

it('page size wraps loading in a bare py-8 main, no role', () => {
  render(<>{renderQueryState(pending, { size: 'page', loading: 'Loading...', error: 'Failed.' })}</>)
  const main = screen.getByText('Loading...')
  expect(main.tagName).toBe('MAIN')
  expect(main.className).toBe('py-8')
  expect(main).not.toHaveAttribute('role')
})

it('page size wraps a first-load error in py-8 with the given role, role never on <main> itself', () => {
  render(
    <>{renderQueryState(loadError, { size: 'page', loading: 'Loading...', error: 'Failed.', role: 'alert' })}</>,
  )
  // An explicit role overrides an element's implicit ARIA role, so the
  // alert must live on an inner element - <main> keeps its landmark role.
  const main = screen.getByRole('main')
  expect(main.className).toBe('py-8')
  expect(main).not.toHaveAttribute('role')
  const alert = screen.getByRole('alert')
  expect(alert).toHaveTextContent('Failed.')
  expect(main).toContainElement(alert)
})

it('page size omits role entirely when not given', () => {
  render(<>{renderQueryState(loadError, { size: 'page', loading: 'Loading...', error: 'Failed.' })}</>)
  expect(screen.getByText('Failed.')).not.toHaveAttribute('role')
})

it('subsection size defaults to bare text-sm text-gray-500, no margin', () => {
  render(<>{renderQueryState(pending, { size: 'subsection', loading: 'Loading...', error: 'Failed.' })}</>)
  const p = screen.getByText('Loading...')
  expect(p.tagName).toBe('P')
  expect(p.className).toBe('text-sm text-gray-500')
})

it('subsection size prepends the caller className ahead of the fixed classes', () => {
  render(
    <>{renderQueryState(pending, { size: 'subsection', className: 'mt-4', loading: 'Loading...', error: 'Failed.' })}</>,
  )
  expect(screen.getByText('Loading...').className).toBe('mt-4 text-sm text-gray-500')
})

it('subsection first-load error is red-700 with role="alert" when role is given', () => {
  render(
    <>{renderQueryState(loadError, { size: 'subsection', className: 'mt-4', loading: 'x', error: 'Failed.', role: 'alert' })}</>,
  )
  const alert = screen.getByRole('alert')
  expect(alert.className).toBe('mt-4 text-sm text-red-700')
})

it('subsection first-load error matches the loading color and carries no role when role is omitted (PricingPanel)', () => {
  render(<>{renderQueryState(loadError, { size: 'subsection', loading: 'x', error: 'Failed.' })}</>)
  const p = screen.getByText('Failed.')
  expect(p.className).toBe('text-sm text-gray-500')
  expect(p).not.toHaveAttribute('role')
})

it('notFound fully replaces the generic error on a first-load error', () => {
  render(
    <>
      {renderQueryState(loadError, {
        size: 'page',
        loading: 'Loading...',
        error: 'Failed.',
        role: 'alert',
        notFound: <div data-testid="not-found">Nothing here.</div>,
      })}
    </>,
  )
  expect(screen.getByTestId('not-found')).toBeInTheDocument()
  expect(screen.queryByText('Failed.')).not.toBeInTheDocument()
})

it('notFound is ignored while pending', () => {
  render(
    <>
      {renderQueryState(pending, {
        size: 'page',
        loading: 'Loading...',
        error: 'Failed.',
        notFound: <div data-testid="not-found">Nothing here.</div>,
      })}
    </>,
  )
  expect(screen.getByText('Loading...')).toBeInTheDocument()
  expect(screen.queryByTestId('not-found')).not.toBeInTheDocument()
})

it('returns undefined once the query is neither pending nor erroring', () => {
  expect(renderQueryState(success, { size: 'page', loading: 'Loading...', error: 'Failed.' })).toBeUndefined()
})

it('returns undefined for a refetch error - data from a prior fetch lets the caller keep its real content', () => {
  expect(
    renderQueryState(refetchError, { size: 'page', loading: 'Loading...', error: 'Failed.', role: 'alert' }),
  ).toBeUndefined()
})

it('ignores notFound on a refetch error - the caller keeps its content, not the notFound surface', () => {
  expect(
    renderQueryState(refetchError, {
      size: 'page',
      loading: 'Loading...',
      error: 'Failed.',
      notFound: <div data-testid="not-found">Nothing here.</div>,
    }),
  ).toBeUndefined()
})

it('refetchWarning is silent while pending', () => {
  expect(refetchWarning(pending)).toBeUndefined()
})

it('refetchWarning is silent on a clean success', () => {
  expect(refetchWarning(success)).toBeUndefined()
})

it('refetchWarning is silent on a first-load error - that state gets the full error UI instead, not a warning', () => {
  expect(refetchWarning(loadError)).toBeUndefined()
})

it('refetchWarning shows a visible, translated, non-interrupting notice on a refetch error', () => {
  renderWithI18n(<>{refetchWarning(refetchError)}</>)
  const status = screen.getByRole('status')
  expect(status).toHaveTextContent(/last refresh failed/i)
  expect(status).toBeVisible()
  expect(screen.queryByRole('alert')).not.toBeInTheDocument()
})
