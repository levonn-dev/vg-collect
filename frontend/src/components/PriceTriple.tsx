import { Trans } from '@lingui/react/macro'

interface PriceTripleProps {
  // Callers already run cents through useDisplayMoney (missing price shows
  // "-", not empty); this owns only the shared text and its one message.
  loose: string
  cib: string
  newPrice: string
  // Only value that differs per caller: gray-500 for search rows, green-800
  // on green for a confirmed match card.
  className: string
}

// Loose/CIB/New price line shared by SearchPicker's rows and PricingPanel's
// match card.
export default function PriceTriple({ loose, cib, newPrice, className }: PriceTripleProps) {
  return (
    <p className={className}>
      <Trans>
        Loose {loose} / CIB {cib} / New{' '}
        {newPrice}
      </Trans>
    </p>
  )
}
