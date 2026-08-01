import { Trans, useLingui } from '@lingui/react/macro'
import type { Dashboard } from '../../api/collection'
import { formatCents } from '../../lib/format'
import { useDisplayMoney } from '../../lib/useDisplayMoney'

export default function StatCards({ dashboard }: { dashboard: Dashboard }) {
  const { t } = useLingui()
  const money = useDisplayMoney()
  const p = dashboard.pricing
  return (
    <section aria-label={t`Totals`} className="grid gap-4 sm:grid-cols-3">
      <div className="rounded border border-gray-200 p-4">
        <p className="text-xs font-semibold uppercase tracking-wide text-gray-500"><Trans>Items</Trans></p>
        <p className="mt-1 text-3xl font-bold">{dashboard.total_entries}</p>
      </div>
      <div className="rounded border border-gray-200 p-4">
        <p className="text-xs font-semibold uppercase tracking-wide text-gray-500"><Trans>Collection value ({money.currency})</Trans></p>
        {p.available ? (
          <>
            <p className="mt-1 text-3xl font-bold">{money.format(p.total_value_cents) ?? money.format(0)}</p>
            <p className="mt-1 text-xs text-gray-500">
              <Trans>{p.priced_entries} priced - {p.unpriced_entries} unpriced - {p.excluded_entries} excluded</Trans>
            </p>
          </>
        ) : (
          <p role="alert" className="mt-1 text-sm text-amber-800">
            <Trans>Value unavailable right now; try again shortly.</Trans>
          </p>
        )}
      </div>
      <div className="rounded border border-gray-200 p-4">
        <p className="text-xs font-semibold uppercase tracking-wide text-gray-500"><Trans>Spent</Trans></p>
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
