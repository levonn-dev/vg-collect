import { Trans, useLingui } from '@lingui/react/macro'
import { CartesianGrid, Line, LineChart, ResponsiveContainer, Tooltip, XAxis, YAxis } from 'recharts'
import type { ValueHistory } from '../../api/collection'
import { useDisplayMoney } from '../../lib/useDisplayMoney'
import SectionLabel from '../SectionLabel'
import { CHART_TOOLTIP_STYLE } from './chartTooltipStyle'

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
      <SectionLabel as="h3" size="xs">
        <Trans>Collection value in {currency} (last 90 days)</Trans>
      </SectionLabel>
      <p className="mb-2 text-xs text-gray-400">
        <Trans>Covers your whole collection; price history does not follow filters.</Trans>
      </p>
      <div className="h-64">
        <ResponsiveContainer width="100%" height="100%">
          <LineChart data={data}>
            <CartesianGrid strokeDasharray="3 3" />
            <XAxis dataKey="date" tick={{ fontSize: 11 }} />
            <YAxis tickFormatter={(v: number) => money.format0(v) ?? ''} tick={{ fontSize: 11 }} />
            <Tooltip
              formatter={(v) => [money.format(Number(v)) ?? '', t`Value`]}
              cursor={{ stroke: 'var(--color-gray-400)' }}
              {...CHART_TOOLTIP_STYLE}
            />
            {/* Theme variable, not a hex: the line follows light/dark. */}
            <Line type="stepAfter" dataKey="cents" stroke="var(--color-gray-900)" dot={false} />
          </LineChart>
        </ResponsiveContainer>
      </div>
      {/* The chart has no text/table fallback otherwise: a screen
          reader gets the section name and the caption text but nothing
          describing what the chart actually shows. */}
      <table className="sr-only">
        <caption>{t`Collection value in ${currency} (last 90 days)`}</caption>
        <thead>
          <tr>
            <th>{t`Date`}</th>
            <th>{t`Value`}</th>
          </tr>
        </thead>
        <tbody>
          {data.map((p) => (
            <tr key={p.date}>
              <td>{p.date}</td>
              <td>{money.format(p.cents) ?? '-'}</td>
            </tr>
          ))}
        </tbody>
      </table>
    </section>
  )
}
