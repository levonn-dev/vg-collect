import { useMutation, useQueryClient, type QueryKey } from '@tanstack/react-query'
import { useState } from 'react'

// useDismissibleAck: the optimistic-dismiss mechanics ApprovalNotice and
// RegionMismatchBanner both need - hide immediately, fire the ack
// mutation, and on success invalidate the query that gates the banner
// (so a remount does not re-flash it). A failed ack rolls the dismiss
// back so the banner reappears with its dismiss button still live,
// rather than leaving the UI silently out of sync with the server.
// invalidateKey is a plain QueryKey (not a caller callback) because
// both current callers do nothing in onSuccess but invalidate one key.
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
