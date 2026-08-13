import { Trans, useLingui } from '@lingui/react/macro'
import { msg } from '@lingui/core/macro'
import type { MessageDescriptor } from '@lingui/core'
import { Bar, BarChart, ResponsiveContainer, Tooltip, XAxis, YAxis } from 'recharts'
import type { Dashboard } from '../../api/collection'
import SectionLabel from '../SectionLabel'
import { CHART_TOOLTIP_STYLE } from './chartTooltipStyle'

// Identity-preserving: by_status/by_item_type row labels have never
// been prettified (the old key.replace('_', ' ') was a no-op for
// every current key - none contain an underscore). An unknown future
// wire value falls back to rendering itself raw.
const statusCountLabels: Record<string, MessageDescriptor> = {
  backlog: msg`backlog`,
  playing: msg`playing`,
  beaten: msg`beaten`,
  completed: msg`completed`,
  dropped: msg`dropped`,
  shelved: msg`shelved`,
}

const itemTypeCountLabels: Record<string, MessageDescriptor> = {
  game: msg`game`,
  console: msg`console`,
  accessory: msg`accessory`,
}

function CountList({
  title, counts, labels,
}: {
  title: string
  counts: Record<string, number>
  labels: Record<string, MessageDescriptor>
}) {
  const { i18n } = useLingui()
  const rows = Object.entries(counts).sort((a, b) => b[1] - a[1])
  return (
    <section aria-label={title} className="rounded border border-gray-200 p-4">
      <SectionLabel as="h3" size="xs" className="mb-2">{title}</SectionLabel>
      <ul className="flex flex-col gap-1 text-sm">
        {rows.map(([key, count]) => (
          <li key={key} className="flex justify-between">
            <span>{labels[key] ? i18n._(labels[key]) : key}</span>
            <span className="font-medium">{count}</span>
          </li>
        ))}
        {rows.length === 0 && <li className="text-gray-400"><Trans>Nothing yet</Trans></li>}
      </ul>
    </section>
  )
}

export default function BreakdownCharts({ dashboard }: { dashboard: Dashboard }) {
  const { t } = useLingui()
  // Top platforms by count; the tail folds visually into the page's
  // filterable list rather than an unreadable chart.
  const platforms = dashboard.by_platform.slice(0, 10)
  return (
    <div className="grid gap-4 md:grid-cols-3">
      <section aria-label={t`By platform`} className="rounded border border-gray-200 p-4 md:col-span-1">
        <SectionLabel as="h3" size="xs" className="mb-2"><Trans>By platform</Trans></SectionLabel>
        <div className="h-64">
          <ResponsiveContainer width="100%" height="100%">
            <BarChart data={platforms} layout="vertical" margin={{ left: 24 }}>
              <XAxis type="number" allowDecimals={false} />
              <YAxis type="category" dataKey="name" width={90} tick={{ fontSize: 11 }} />
              <Tooltip cursor={{ fill: 'var(--color-gray-100)' }} {...CHART_TOOLTIP_STYLE} />
              {/* Theme variable, not a hex: the bar follows light/dark. */}
              <Bar dataKey="count" fill="var(--color-gray-900)" />
            </BarChart>
          </ResponsiveContainer>
        </div>
      </section>
      <CountList title={t`By status`} counts={dashboard.by_status} labels={statusCountLabels} />
      <CountList title={t`By item type`} counts={dashboard.by_item_type} labels={itemTypeCountLabels} />
    </div>
  )
}
