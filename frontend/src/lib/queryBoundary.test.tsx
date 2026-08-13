import { render, screen } from '@testing-library/react'
import { renderQueryState } from './queryBoundary'

const pending = { isPending: true, isError: false }
const errored = { isPending: false, isError: true }
const success = { isPending: false, isError: false }

it('page size wraps loading in a bare py-8 main, no role', () => {
  render(<>{renderQueryState(pending, { size: 'page', loading: 'Loading...', error: 'Failed.' })}</>)
  const main = screen.getByText('Loading...')
  expect(main.tagName).toBe('MAIN')
  expect(main.className).toBe('py-8')
  expect(main).not.toHaveAttribute('role')
})

it('page size wraps the error in py-8 with the given role', () => {
  render(
    <>{renderQueryState(errored, { size: 'page', loading: 'Loading...', error: 'Failed.', role: 'alert' })}</>,
  )
  const main = screen.getByRole('alert')
  expect(main.tagName).toBe('MAIN')
  expect(main.className).toBe('py-8')
  expect(main).toHaveTextContent('Failed.')
})

it('page size omits role entirely when not given', () => {
  render(<>{renderQueryState(errored, { size: 'page', loading: 'Loading...', error: 'Failed.' })}</>)
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

it('subsection error is red-700 with role="alert" when role is given', () => {
  render(
    <>{renderQueryState(errored, { size: 'subsection', className: 'mt-4', loading: 'x', error: 'Failed.', role: 'alert' })}</>,
  )
  const alert = screen.getByRole('alert')
  expect(alert.className).toBe('mt-4 text-sm text-red-700')
})

it('subsection error matches the loading color and carries no role when role is omitted (PricingPanel)', () => {
  render(<>{renderQueryState(errored, { size: 'subsection', loading: 'x', error: 'Failed.' })}</>)
  const p = screen.getByText('Failed.')
  expect(p.className).toBe('text-sm text-gray-500')
  expect(p).not.toHaveAttribute('role')
})

it('notFound fully replaces the generic error once the query errors', () => {
  render(
    <>
      {renderQueryState(errored, {
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
