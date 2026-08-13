import { QueryClient } from '@tanstack/react-query'
import { invalidateEntryQueries } from './entryQueries'

function client() {
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } })
  return { qc, spy: vi.spyOn(qc, 'invalidateQueries') }
}

it('invalidates entries, dashboard, and recommendations in that order with no extras', () => {
  const { qc, spy } = client()
  invalidateEntryQueries(qc)
  expect(spy.mock.calls.map((c) => c[0]?.queryKey)).toEqual([
    ['entries'],
    ['dashboard'],
    ['recommendations'],
  ])
})

it('appends per-site extras after the triple, in the order given', () => {
  const { qc, spy } = client()
  invalidateEntryQueries(qc, [['entry-facets'], ['tags']])
  expect(spy.mock.calls.map((c) => c[0]?.queryKey)).toEqual([
    ['entries'],
    ['dashboard'],
    ['recommendations'],
    ['entry-facets'],
    ['tags'],
  ])
})

it('passes a multi-part extra key through untouched', () => {
  const { qc, spy } = client()
  invalidateEntryQueries(qc, [['product', 'p1']])
  expect(spy.mock.calls.map((c) => c[0]?.queryKey)).toEqual([
    ['entries'],
    ['dashboard'],
    ['recommendations'],
    ['product', 'p1'],
  ])
})
