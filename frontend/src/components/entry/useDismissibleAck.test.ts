import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { act, renderHook, waitFor } from '@testing-library/react'
import { createElement, type ReactNode } from 'react'
import { useDismissibleAck } from './useDismissibleAck'

// Plain .ts (no JSX): the wrapper below uses createElement instead.
function setup(mutationFn: () => Promise<unknown>) {
  const qc = new QueryClient({ defaultOptions: { mutations: { retry: false } } })
  const invalidateSpy = vi.spyOn(qc, 'invalidateQueries')
  const wrapper = ({ children }: { children: ReactNode }) =>
    createElement(QueryClientProvider, { client: qc }, children)
  const hook = renderHook(() => useDismissibleAck(mutationFn, ['thing', 'id1']), { wrapper })
  return { hook, invalidateSpy }
}

it('starts not dismissed', () => {
  const { hook } = setup(() => Promise.resolve())
  expect(hook.result.current.dismissed).toBe(false)
})

it('dismiss optimistically flips dismissed, then fires the mutation', async () => {
  const mutationFn = vi.fn().mockResolvedValue(undefined)
  const { hook } = setup(mutationFn)
  act(() => hook.result.current.dismiss())
  // dismissed flips synchronously with the click, ahead of the mutation starting.
  expect(hook.result.current.dismissed).toBe(true)
  await waitFor(() => expect(mutationFn).toHaveBeenCalledTimes(1))
})

it('a successful ack invalidates the caller-supplied query key', async () => {
  const { hook, invalidateSpy } = setup(() => Promise.resolve())
  act(() => hook.result.current.dismiss())
  await waitFor(() => expect(invalidateSpy).toHaveBeenCalledWith({ queryKey: ['thing', 'id1'] }))
})

it('a failed ack rolls dismissed back to false so the notice reappears', async () => {
  const { hook, invalidateSpy } = setup(() => Promise.reject(new Error('boom')))
  act(() => hook.result.current.dismiss())
  expect(hook.result.current.dismissed).toBe(true)
  await waitFor(() => expect(hook.result.current.dismissed).toBe(false))
  expect(invalidateSpy).not.toHaveBeenCalled()
})
