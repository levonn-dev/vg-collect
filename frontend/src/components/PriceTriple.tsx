import { Trans } from '@lingui/react/macro'

interface PriceTripleProps {
  // Each caller has already run its own three cents fields through
  // useDisplayMoney (a missing price shows "-", not empty) - this
  // component only owns the shared "Loose X / CIB Y / New Z" text and
  // its one translatable message.
  loose: string
  cib: string
  newPrice: string
  // The one thing that differs per site: a search result row (plain
  // gray-500 text) and a confirmed price match card (green-800 on a
  // green card, with the top margin its neighbor above does not need).
  className: string
}

// PriceTriple renders the Loose/CIB/New price line shared by
// SearchPicker's result rows and PricingPanel's confirmed-match card.
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
