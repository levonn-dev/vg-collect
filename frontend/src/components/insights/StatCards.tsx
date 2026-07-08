import type { Dashboard } from '../../api/collection'
import { formatCents } from '../../lib/format'

export default function StatCards({ dashboard }: { dashboard: Dashboard }) {
  const p = dashboard.pricing
  return (
    <section aria-label="Totals" className="grid gap-4 sm:grid-cols-3">
      <div className="rounded border border-gray-200 p-4">
        <p className="text-xs font-semibold uppercase tracking-wide text-gray-500">Items</p>
        <p className="mt-1 text-3xl font-bold">{dashboard.total_entries}</p>
      </div>
      <div className="rounded border border-gray-200 p-4">
        <p className="text-xs font-semibold uppercase tracking-wide text-gray-500">Collection value (USD)</p>
        {p.available ? (
          <>
            <p className="mt-1 text-3xl font-bold">{formatCents(p.total_value_cents) ?? '$0.00'}</p>
            <p className="mt-1 text-xs text-gray-500">
              {p.priced_entries} priced - {p.unpriced_entries} unpriced - {p.excluded_entries} excluded
            </p>
          </>
        ) : (
          <p role="alert" className="mt-1 text-sm text-amber-800">
            Value unavailable right now; try again shortly.
          </p>
        )}
      </div>
      <div className="rounded border border-gray-200 p-4">
        <p className="text-xs font-semibold uppercase tracking-wide text-gray-500">Spent</p>
        {dashboard.spend.length === 0 ? (
          <p className="mt-1 text-sm text-gray-500">No purchase prices recorded.</p>
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
