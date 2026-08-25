import { Trans, useLingui } from '@lingui/react/macro'
import { keepPreviousData, useQuery } from '@tanstack/react-query'
import { useState } from 'react'
import { fetchDashboard, fetchValueHistory } from '../../api/collection'
import type { ListState } from '../../lib/listParams'
import { toFilterQuery } from '../../lib/listParams'
import { refetchWarning, renderQueryState } from '../../lib/queryBoundary'
import BreakdownCharts from './BreakdownCharts'
import RecsPanel from './RecsPanel'
import StatCards from './StatCards'
import ValueOverTime from './ValueOverTime'

// Stat cards ride above the list and follow active filters; heavier
// charts/suggestions expand on demand and fetch only once expanded.
// Value-over-time alone stays whole-collection (aggregate snapshots).
export default function InsightsPanel({ state }: { state: ListState }) {
  const { t } = useLingui()
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

  return (
    <section aria-label={t`Insights`} className="mb-4 flex flex-col gap-4">
      {dashboard.data !== undefined ? (
        <>
          {refetchWarning(dashboard)}
          <StatCards dashboard={dashboard.data} />
        </>
      ) : (
        renderQueryState(dashboard, {
          size: 'subsection',
          role: 'alert',
          loading: <Trans>Loading stats...</Trans>,
          error: <Trans>Stats cannot be loaded right now.</Trans>,
        })
      )}
      <button
        type="button"
        onClick={() => setOpen(!open)}
        aria-expanded={open}
        className="self-start text-sm text-gray-600 underline hover:text-gray-900"
      >
        {open ? t`Hide insights` : t`Show insights`}
      </button>
      {open && dashboard.data && (
        <>
          <BreakdownCharts dashboard={dashboard.data} />
          {history.data && <ValueOverTime history={history.data} />}
          {history.isError && (
            <p role="alert" className="rounded bg-amber-50 p-3 text-sm text-amber-800">
              <Trans>Value history cannot be loaded right now.</Trans>
            </p>
          )}
          <RecsPanel />
        </>
      )}
    </section>
  )
}
