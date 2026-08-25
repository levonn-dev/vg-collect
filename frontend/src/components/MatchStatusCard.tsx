import { Trans } from '@lingui/react/macro'
import type { ReactNode } from 'react'
import type { Product } from '../api/catalog'
import { useDisplayMoney } from '../lib/useDisplayMoney'
import PriceTriple from './PriceTriple'

interface MatchStatusCardProps {
  pc: NonNullable<Product['pricecharting']>
  // No default: PricingPanel passes true, the wizard's confirm step passes
  // false (no entry yet); a default would let one site drift silently.
  showPrices: boolean
  // Trailing content each caller owns (ConfirmStep's Change-listing button);
  // not shared with PricingPanel, so it stays out of a dedicated prop.
  children?: ReactNode
}

// Renders entirely through Trans, which subscribes to the locale context
// itself, so it stays live without depending on the caller re-rendering.
export default function MatchStatusCard({ pc, showPrices, children }: MatchStatusCardProps) {
  const money = useDisplayMoney()
  const pcName = pc.pc_name
  const consoleName = pc.console_name
  const confidence = Math.round(pc.match_confidence * 100)
  return (
    <div className="rounded bg-green-50 p-3 text-sm text-green-800">
      <p>
        {pc.verified ? (
          <Trans>
            Priced as "{pcName}" ({consoleName}) - match {confidence}%, verified.
          </Trans>
        ) : (
          <Trans>
            Priced as "{pcName}" ({consoleName}) - match {confidence}%.
          </Trans>
        )}
      </p>
      {showPrices && (
        <PriceTriple
          loose={money.format(pc.loose_cents) ?? '-'}
          cib={money.format(pc.cib_cents) ?? '-'}
          newPrice={money.format(pc.new_cents) ?? '-'}
          className="mt-1 text-xs text-green-800"
        />
      )}
      {children}
    </div>
  )
}
