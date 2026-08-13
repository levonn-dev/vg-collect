import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { renderHook, waitFor } from '@testing-library/react'
import { createElement, type ReactNode } from 'react'
import { jsonResponse, problemResponse } from '../../test/fixtures'
import { useSubmission } from './useSubmission'

// Plain .ts (no JSX): the wrapper below uses createElement instead.
function wrapper(qc: QueryClient) {
  return ({ children }: { children: ReactNode }) => createElement(QueryClientProvider, { client: qc }, children)
}

afterEach(() => {
  vi.unstubAllGlobals()
})

it('resolves the submission on a normal 200', async () => {
  const fetchMock = vi.fn().mockResolvedValue(
    jsonResponse(200, { id: 's1', entry_id: 'e1', status: 'pending', created_at: 'x', updated_at: 'x' }),
  )
  vi.stubGlobal('fetch', fetchMock)
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } })
  const { result } = renderHook(() => useSubmission('e1'), { wrapper: wrapper(qc) })
  await waitFor(() => expect(result.current.data?.status).toBe('pending'))
  expect(fetchMock).toHaveBeenCalledWith('/api/entries/e1/submission')
})

it('resolves null, not an error, on a 404 (never submitted)', async () => {
  const fetchMock = vi.fn().mockResolvedValue(problemResponse(404, 'submission_not_found', 'x'))
  vi.stubGlobal('fetch', fetchMock)
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } })
  const { result } = renderHook(() => useSubmission('e1'), { wrapper: wrapper(qc) })
  await waitFor(() => expect(result.current.isSuccess).toBe(true))
  expect(result.current.data).toBeNull()
})

it('surfaces a non-404 failure as an error, not null', async () => {
  const fetchMock = vi.fn().mockResolvedValue(problemResponse(500, 'internal', 'x'))
  vi.stubGlobal('fetch', fetchMock)
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } })
  const { result } = renderHook(() => useSubmission('e1'), { wrapper: wrapper(qc) })
  await waitFor(() => expect(result.current.isError).toBe(true))
})
