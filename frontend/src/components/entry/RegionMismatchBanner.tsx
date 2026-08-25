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

// Dismissing stamps region_mismatch_ack_at server-side for the CURRENT
// (region, product) pair, so the banner reappears if either changes.
// Shares useDismissibleAck's mechanics and DismissibleNotice's markup with
// ApprovalNotice; only the initial-data query and ack call differ.
export default function RegionMismatchBanner({ entryId, productId, region, regionMismatchAckAt }: RegionMismatchBannerProps) {
  const { t } = useLingui()
  // Same query key/retry as PricingPanel's product fetch (not ApprovalNotice's
  // retry:false), so both dedupe one fetch when mounted together; the render
  // guard below already keeps a pending/error read from flashing.
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
