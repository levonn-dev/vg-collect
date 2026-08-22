import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { renderHook, waitFor } from '@testing-library/react'
import { createElement, type ReactNode } from 'react'
import { calledPath, meFixture } from '../test/fixtures'
import { useMe } from './useMe'

// Plain .ts (no JSX): the wrapper below uses createElement instead.
function wrapper(qc: QueryClient) {
  return ({ children }: { children: ReactNode }) => createElement(QueryClientProvider, { client: qc }, children)
}

it('reads a seeded ["me"] cache entry synchronously', () => {
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } })
  qc.setQueryData(['me'], meFixture({ handle: 'Alice' }))
  const { result } = renderHook(() => useMe(), { wrapper: wrapper(qc) })
  expect(result.current.data?.handle).toBe('Alice')
})

it('fetches through fetchMe on a cold cache', async () => {
  const fetchMock = vi.fn().mockResolvedValue(
    new Response(JSON.stringify(meFixture({ handle: 'Bob' })), {
      status: 200,
      headers: { 'Content-Type': 'application/json' },
    }),
  )
  vi.stubGlobal('fetch', fetchMock)
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } })
  const { result } = renderHook(() => useMe(), { wrapper: wrapper(qc) })
  await waitFor(() => expect(result.current.data?.handle).toBe('Bob'))
  expect(calledPath(fetchMock, 0)).toBe('/api/me')
  vi.unstubAllGlobals()
})
