import { useQuery } from '@tanstack/react-query'
import { fetchDashboard, fetchValueHistory } from '../api/collection'
import BreakdownCharts from '../components/dashboard/BreakdownCharts'
import RecsPanel from '../components/dashboard/RecsPanel'
import StatCards from '../components/dashboard/StatCards'
import ValueOverTime from '../components/dashboard/ValueOverTime'

export default function Dashboard() {
  const dashboard = useQuery({ queryKey: ['dashboard'], queryFn: fetchDashboard })
  const history = useQuery({ queryKey: ['dashboard', 'value-history'], queryFn: fetchValueHistory })

  if (dashboard.isPending) return <main className="py-8">Crunching the numbers...</main>
  if (dashboard.isError) {
    return (
      <main className="py-8" role="alert">
        The dashboard cannot be loaded right now. Please try again.
      </main>
    )
  }

  return (
    <main className="flex flex-col gap-4 py-6" aria-label="Dashboard">
      <StatCards dashboard={dashboard.data} />
      <BreakdownCharts dashboard={dashboard.data} />
      {history.isSuccess && <ValueOverTime history={history.data} />}
      {history.isError && (
        <p role="alert" className="rounded bg-amber-50 p-3 text-sm text-amber-800">
          Value history cannot be loaded right now.
        </p>
      )}
      <RecsPanel />
    </main>
  )
}
