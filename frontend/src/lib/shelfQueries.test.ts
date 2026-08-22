import { QueryClient } from '@tanstack/react-query'
import { invalidateShelfSocial } from './shelfQueries'

function client() {
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } })
  return { qc, spy: vi.spyOn(qc, 'invalidateQueries') }
}

it('invalidates the shelf comments and summary keys, scoped to the given shelf', () => {
  const { qc, spy } = client()
  invalidateShelfSocial(qc, 's1')
  expect(spy.mock.calls.map((c) => c[0]?.queryKey)).toEqual([
    ['shelfComments', 's1'],
    ['shelfSummary', 's1'],
  ])
})
