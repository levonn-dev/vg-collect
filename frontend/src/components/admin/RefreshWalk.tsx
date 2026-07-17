import { useMutation } from '@tanstack/react-query'
import { triggerRefresh } from '../../api/admin'
import { ApiError } from '../../api/client'

function refreshErrorMessage(e: unknown): string {
  if (e instanceof ApiError && e.code === 'refresh_in_progress') return 'A walk is already running.'
  return 'The trigger failed - try again.'
}

// RefreshWalk fires the same walk the nightly CronJob runs; the 202
// comes back immediately and the walk proceeds detached.
export default function RefreshWalk() {
  const run = useMutation({ mutationFn: triggerRefresh })
  return (
    <section aria-label="Refresh walk" className="mt-6">
      <h3 className="text-base font-semibold">Refresh walk</h3>
      <button
        type="button"
        onClick={() => run.mutate()}
        disabled={run.isPending}
        className="mt-2 rounded border border-gray-300 px-3 py-1 text-sm hover:bg-gray-50 disabled:opacity-50"
      >
        Trigger refresh walk
      </button>
      {run.isSuccess && <p className="mt-2 text-sm text-gray-700">Walk started.</p>}
      {run.isError && (
        <p role="alert" className="mt-2 text-sm text-red-700">
          {refreshErrorMessage(run.error)}
        </p>
      )}
    </section>
  )
}
