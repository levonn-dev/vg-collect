import { Trans, useLingui } from '@lingui/react/macro'
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { useState } from 'react'
import { fetchProduct } from '../../api/catalog'
import { ackRegionMismatch } from '../../api/collection'
import { regionMismatch } from '../../lib/productTitle'

interface RegionMismatchBannerProps {
  entryId: string
  productId: string
  region: string
  regionMismatchAckAt?: string
}

// RegionMismatchBanner flags an entry whose matched price listing
// prices a different region than the entry itself: a hand-picked
// match, or an entry left over from before region-aware matching.
// Dismissing stamps region_mismatch_ack_at server-side for the
// CURRENT (region, product) choice, so the banner does not reappear
// until either changes there. Mirrors ApprovalNotice's mechanics:
// optimistic dismiss, onError rollback, query invalidation.
export default function RegionMismatchBanner({ entryId, productId, region, regionMismatchAckAt }: RegionMismatchBannerProps) {
  const { t } = useLingui()
  const queryClient = useQueryClient()
  // Same query key and retry semantics as PricingPanel's own product
  // fetch, deliberately not ApprovalNotice's retry:false: sharing the
  // cache entry lets the two dedupe one fetch instead of two when both
  // are mounted for the same entry, and the render guard below already
  // keeps a pending/error read from flashing anything.
  const product = useQuery({
    queryKey: ['product', productId],
    queryFn: () => fetchProduct(productId),
  })
  const [dismissed, setDismissed] = useState(false)
  const ack = useMutation({
    mutationFn: () => ackRegionMismatch(entryId),
    onSuccess: () => void queryClient.invalidateQueries({ queryKey: ['entry', entryId] }),
    onError: () => setDismissed(false),
  })

  if (product.isPending || product.isError) return null
  const consoleName = product.data.pricecharting?.console_name
  if (!consoleName || !regionMismatch(consoleName, region) || regionMismatchAckAt || dismissed) return null
  return (
    <div role="status" className="mb-4 flex items-start justify-between gap-3 rounded bg-amber-50 p-3 text-sm text-amber-800">
      <p><Trans>This price listing is from a different region than this entry.</Trans></p>
      <button
        type="button"
        aria-label={t`Dismiss region mismatch notice`}
        onClick={() => {
          setDismissed(true)
          ack.mutate()
        }}
        className="shrink-0 rounded border border-amber-300 px-2 py-0.5 hover:bg-white"
      >
        <Trans>Dismiss</Trans>
      </button>
    </div>
  )
}
