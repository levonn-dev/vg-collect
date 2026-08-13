import { Trans, useLingui } from '@lingui/react/macro'
import { useQuery } from '@tanstack/react-query'
import { fetchProduct } from '../../api/catalog'
import { ackRegionMismatch } from '../../api/collection'
import { regionMismatch } from '../../lib/productTitle'
import DismissibleNotice from './DismissibleNotice'
import { useDismissibleAck } from './useDismissibleAck'

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
// until either changes there. Shares useDismissibleAck's optimistic
// -dismiss-with-rollback mechanics and DismissibleNotice's markup with
// ApprovalNotice; only the initial-data query and the ack call differ.
export default function RegionMismatchBanner({ entryId, productId, region, regionMismatchAckAt }: RegionMismatchBannerProps) {
  const { t } = useLingui()
  // Same query key and retry semantics as PricingPanel's own product
  // fetch, deliberately not ApprovalNotice's retry:false: sharing the
  // cache entry lets the two dedupe one fetch instead of two when both
  // are mounted for the same entry, and the render guard below already
  // keeps a pending/error read from flashing anything.
  const product = useQuery({
    queryKey: ['product', productId],
    queryFn: () => fetchProduct(productId),
  })
  const { dismissed, dismiss } = useDismissibleAck(
    () => ackRegionMismatch(entryId),
    ['entry', entryId],
  )

  if (product.isPending || product.isError) return null
  const consoleName = product.data.pricecharting?.console_name
  if (!consoleName || !regionMismatch(consoleName, region) || regionMismatchAckAt || dismissed) return null
  return (
    <DismissibleNotice tone="amber" dismissLabel={t`Dismiss region mismatch notice`} onDismiss={dismiss}>
      <Trans>This price listing is from a different region than this entry.</Trans>
    </DismissibleNotice>
  )
}
