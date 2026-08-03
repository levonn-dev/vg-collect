import { Trans, useLingui } from '@lingui/react/macro'
import { CartesianGrid, Line, LineChart, ResponsiveContainer, Tooltip, XAxis, YAxis } from 'recharts'
import type { ValueHistory } from '../../api/collection'
import { useDisplayMoney } from '../../lib/useDisplayMoney'

export default function ValueOverTime({ history }: { history: ValueHistory }) {
  const { t } = useLingui()
  const money = useDisplayMoney()
  if (!history.available) {
    return (
      <p role="alert" className="rounded bg-amber-50 p-3 text-sm text-amber-800">
        <Trans>Value history is temporarily unavailable; try again shortly.</Trans>
      </p>
    )
  }
  if (history.points.length === 0) {
    return (
      <p className="rounded bg-gray-50 p-3 text-sm text-gray-600">
        <Trans>Value history appears when price snapshots accumulate for your collection.</Trans>
      </p>
    )
  }
  const data = history.points.map((p) => ({ date: p.date, cents: p.value_cents }))
  const currency = money.currency
  return (
    <section aria-label={t`Collection value over time`} className="rounded border border-gray-200 p-4">
      <h3 className="text-xs font-semibold uppercase tracking-wide text-gray-500">
        <Trans>Collection value in {currency} (last 90 days)</Trans>
      </h3>
      <p className="mb-2 text-xs text-gray-400">
        <Trans>Covers your whole collection; price history does not follow filters.</Trans>
      </p>
      <div className="h-64">
        <ResponsiveContainer width="100%" height="100%">
          <LineChart data={data}>
            <CartesianGrid strokeDasharray="3 3" />
            <XAxis dataKey="date" tick={{ fontSize: 11 }} />
            <YAxis tickFormatter={(v: number) => money.format0(v) ?? ''} tick={{ fontSize: 11 }} />
            {/* Recharts' default hover line and tooltip box are
                hardcoded light; theme variables keep the mouse-over
                readable in dark mode. */}
            <Tooltip
              formatter={(v) => [money.format(Number(v)) ?? '', t`Value`]}
              cursor={{ stroke: 'var(--color-gray-400)' }}
              contentStyle={{ backgroundColor: 'var(--color-white)', border: '1px solid var(--color-gray-300)' }}
              labelStyle={{ color: 'var(--color-gray-900)' }}
            />
            {/* Theme variable, not a hex: the line follows light/dark. */}
            <Line type="stepAfter" dataKey="cents" stroke="var(--color-gray-900)" dot={false} />
          </LineChart>
        </ResponsiveContainer>
      </div>
    </section>
  )
}
