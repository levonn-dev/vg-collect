import { Trans, useLingui } from '@lingui/react/macro'
import type { Dashboard } from '../../api/collection'
import { formatCents } from '../../lib/format'
import { useDisplayMoney } from '../../lib/useDisplayMoney'
import SectionLabel from '../SectionLabel'

export default function StatCards({ dashboard }: { dashboard: Dashboard }) {
  const { t } = useLingui()
  const money = useDisplayMoney()
  const p = dashboard.pricing
  const currency = money.currency
  const pricedCount = p.priced_entries
  const unpricedCount = p.unpriced_entries
  const excludedCount = p.excluded_entries
  return (
    <section aria-label={t`Totals`} className="grid gap-4 sm:grid-cols-3">
      <div className="rounded border border-gray-200 p-4">
        <SectionLabel as="p" size="xs"><Trans>Items</Trans></SectionLabel>
        <p className="mt-1 text-3xl font-bold">{dashboard.total_entries}</p>
      </div>
      <div className="rounded border border-gray-200 p-4">
        <SectionLabel as="p" size="xs"><Trans>Collection value ({currency})</Trans></SectionLabel>
        {p.available ? (
          <>
            <p className="mt-1 text-3xl font-bold">{money.format(p.total_value_cents) ?? money.format(0)}</p>
            <p className="mt-1 text-xs text-gray-500">
              <Trans>{pricedCount} priced - {unpricedCount} unpriced - {excludedCount} excluded</Trans>
            </p>
          </>
        ) : (
          <p role="alert" className="mt-1 text-sm text-amber-800">
            <Trans>Value unavailable right now; try again shortly.</Trans>
          </p>
        )}
      </div>
      <div className="rounded border border-gray-200 p-4">
        <SectionLabel as="p" size="xs"><Trans>Spent</Trans></SectionLabel>
        {dashboard.spend.length === 0 ? (
          <p className="mt-1 text-sm text-gray-500"><Trans>No purchase prices recorded.</Trans></p>
        ) : (
          dashboard.spend.map((s) => (
            <p key={s.currency} className="mt-1 text-2xl font-bold">
              {formatCents(s.total_cents, s.currency)}
            </p>
          ))
        )}
      </div>
    </section>
  )
}
