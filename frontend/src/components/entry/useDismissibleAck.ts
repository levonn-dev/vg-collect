import { useMutation, useQueryClient, type QueryKey } from '@tanstack/react-query'
import { useState } from 'react'

// Hides immediately, fires the ack mutation, and invalidates invalidateKey on
// success (no re-flash on remount). A failed ack rolls the dismiss back so the
// banner reappears with its dismiss button live, not silently out of sync.
// invalidateKey is a plain QueryKey since both callers just invalidate one key.
export function useDismissibleAck(mutationFn: () => Promise<unknown>, invalidateKey: QueryKey) {
  const queryClient = useQueryClient()
  const [dismissed, setDismissed] = useState(false)
  const ack = useMutation({
    mutationFn,
    onSuccess: () => void queryClient.invalidateQueries({ queryKey: invalidateKey }),
    onError: () => setDismissed(false),
  })
  const dismiss = () => {
    setDismissed(true)
    ack.mutate()
  }
  return { dismissed, dismiss }
}
