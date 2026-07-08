import { keepPreviousData, useQuery } from '@tanstack/react-query'
import { useState } from 'react'
import { fetchDashboard, fetchValueHistory } from '../../api/collection'
import type { ListState } from '../../lib/listParams'
import { toFilterQuery } from '../../lib/listParams'
import BreakdownCharts from './BreakdownCharts'
import RecsPanel from './RecsPanel'
import StatCards from './StatCards'
import ValueOverTime from './ValueOverTime'

// InsightsPanel is the dashboard folded into the collection page: the
// stat cards always ride above the list and follow the active
// filters; the heavier charts and suggestions expand on demand (and
// only fetch once expanded). Value-over-time alone stays
// whole-collection - price snapshots record aggregate history - and
// says so in its caption.
export default function InsightsPanel({ state }: { state: ListState }) {
  const filterQuery = toFilterQuery(state).toString()
  const [open, setOpen] = useState(false)
  const dashboard = useQuery({
    queryKey: ['dashboard', filterQuery],
    queryFn: () => fetchDashboard(toFilterQuery(state)),
    placeholderData: keepPreviousData,
  })
  const history = useQuery({
    queryKey: ['dashboard', 'value-history'],
    queryFn: fetchValueHistory,
    enabled: open,
  })

  if (dashboard.isError) {
    return (
      <p role="alert" className="mb-4 text-sm text-gray-500">
        Stats cannot be loaded right now.
      </p>
    )
  }
  return (
    <section aria-label="Insights" className="mb-4 flex flex-col gap-4">
      {dashboard.data ? (
        <StatCards dashboard={dashboard.data} />
      ) : (
        <p className="text-sm text-gray-500">Loading stats...</p>
      )}
      <button
        type="button"
        onClick={() => setOpen(!open)}
        aria-expanded={open}
        className="self-start text-sm text-gray-600 underline hover:text-gray-900"
      >
        {open ? 'Hide insights' : 'Show insights'}
      </button>
      {open && dashboard.data && (
        <>
          <BreakdownCharts dashboard={dashboard.data} />
          {history.data && <ValueOverTime history={history.data} />}
          {history.isError && (
            <p role="alert" className="rounded bg-amber-50 p-3 text-sm text-amber-800">
              Value history cannot be loaded right now.
            </p>
          )}
          <RecsPanel />
        </>
      )}
    </section>
  )
}
