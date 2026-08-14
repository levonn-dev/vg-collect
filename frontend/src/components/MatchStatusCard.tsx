import { Trans } from '@lingui/react/macro'
import type { ReactNode } from 'react'
import type { Product } from '../api/catalog'
import { useDisplayMoney } from '../lib/useDisplayMoney'
import PriceTriple from './PriceTriple'

interface MatchStatusCardProps {
  pc: NonNullable<Product['pricecharting']>
  // No default: PricingPanel's own price display passes true, the add
  // wizard's confirm step passes false (prices never show there before
  // the entry exists) - a default would let one site drift silently.
  showPrices: boolean
  // Trailing content each call site owns (ConfirmStep's "Change
  // listing" button); that button is not a byte-identical twin of
  // anything on the PricingPanel side, so it stays out of this
  // component rather than becoming a second prop.
  children?: ReactNode
}

// MatchStatusCard is the green "Priced as ..." status card shared by
// the entry page's pricing panel and the add wizard's confirm step.
// Renders entirely through Trans, which subscribes to the locale
// context itself and does not depend on its caller re-rendering to
// stay live (contrast rowMeta.tsx, a non-component helper with no such
// subscription of its own).
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
