import { CartesianGrid, Line, LineChart, ResponsiveContainer, Tooltip, XAxis, YAxis } from 'recharts'
import type { ValueHistory } from '../../api/collection'

export default function ValueOverTime({ history }: { history: ValueHistory }) {
  if (!history.available) {
    return (
      <p role="alert" className="rounded bg-amber-50 p-3 text-sm text-amber-800">
        Value history is temporarily unavailable (pricing service unreachable).
      </p>
    )
  }
  if (history.points.length === 0) {
    return (
      <p className="rounded bg-gray-50 p-3 text-sm text-gray-600">
        Value history appears once price snapshots accumulate for your collection.
      </p>
    )
  }
  const data = history.points.map((p) => ({ date: p.date, dollars: p.value_cents / 100 }))
  return (
    <section aria-label="Collection value over time" className="rounded border border-gray-200 p-4">
      <h3 className="mb-2 text-xs font-semibold uppercase tracking-wide text-gray-500">
        Collection value (last 90 days)
      </h3>
      <div className="h-64">
        <ResponsiveContainer width="100%" height="100%">
          <LineChart data={data}>
            <CartesianGrid strokeDasharray="3 3" />
            <XAxis dataKey="date" tick={{ fontSize: 11 }} />
            <YAxis tickFormatter={(v: number) => `$${v.toFixed(0)}`} tick={{ fontSize: 11 }} />
            <Tooltip formatter={(v) => [`$${Number(v).toFixed(2)}`, 'Value']} />
            <Line type="stepAfter" dataKey="dollars" stroke="#111827" dot={false} />
          </LineChart>
        </ResponsiveContainer>
      </div>
    </section>
  )
}
