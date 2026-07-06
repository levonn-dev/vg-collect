import { Bar, BarChart, ResponsiveContainer, Tooltip, XAxis, YAxis } from 'recharts'
import type { Dashboard } from '../../api/collection'

function CountList({ title, counts }: { title: string; counts: Record<string, number> }) {
  const rows = Object.entries(counts).sort((a, b) => b[1] - a[1])
  return (
    <section aria-label={title} className="rounded border border-gray-200 p-4">
      <h3 className="mb-2 text-xs font-semibold uppercase tracking-wide text-gray-500">{title}</h3>
      <ul className="flex flex-col gap-1 text-sm">
        {rows.map(([key, count]) => (
          <li key={key} className="flex justify-between">
            <span>{key.replace('_', ' ')}</span>
            <span className="font-medium">{count}</span>
          </li>
        ))}
        {rows.length === 0 && <li className="text-gray-400">Nothing yet</li>}
      </ul>
    </section>
  )
}

export default function BreakdownCharts({ dashboard }: { dashboard: Dashboard }) {
  // Top platforms by count; the tail folds visually into the page's
  // filterable list rather than an unreadable chart.
  const platforms = dashboard.by_platform.slice(0, 10)
  return (
    <div className="grid gap-4 md:grid-cols-3">
      <section aria-label="By platform" className="rounded border border-gray-200 p-4 md:col-span-1">
        <h3 className="mb-2 text-xs font-semibold uppercase tracking-wide text-gray-500">By platform</h3>
        <div className="h-64">
          <ResponsiveContainer width="100%" height="100%">
            <BarChart data={platforms} layout="vertical" margin={{ left: 24 }}>
              <XAxis type="number" allowDecimals={false} />
              <YAxis type="category" dataKey="name" width={90} tick={{ fontSize: 11 }} />
              <Tooltip />
              <Bar dataKey="count" fill="#111827" />
            </BarChart>
          </ResponsiveContainer>
        </div>
      </section>
      <CountList title="By status" counts={dashboard.by_status} />
      <CountList title="By item type" counts={dashboard.by_item_type} />
    </div>
  )
}
