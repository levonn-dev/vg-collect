import { Trans, useLingui } from '@lingui/react/macro'
import { t } from '@lingui/core/macro'
import type { I18n } from '@lingui/core'
import { useMutation } from '@tanstack/react-query'
import { triggerRefresh } from '../../api/admin'
import { ApiError } from '../../api/client'

// t(i18n) throughout this file, component included: refreshErrorMessage
// is a plain function (cannot call useLingui() itself, same reasoning as
// rowMeta.tsx), so it takes the caller's i18n explicitly. The component
// below uses the identical explicit form for its own strings rather than
// importing a second, same-named t from the react macro.
function refreshErrorMessage(e: unknown, i18n: I18n): string {
  if (e instanceof ApiError && e.code === 'refresh_in_progress') return t(i18n)`A walk is already running.`
  return t(i18n)`The trigger failed - try again.`
}

// RefreshWalk fires the same walk the nightly CronJob runs; the 202
// comes back immediately and the walk proceeds detached.
export default function RefreshWalk() {
  const { i18n } = useLingui()
  const run = useMutation({ mutationFn: triggerRefresh })
  return (
    <section aria-label={t(i18n)`Refresh walk`} className="mt-6">
      <h3 className="text-base font-semibold"><Trans>Refresh walk</Trans></h3>
      <button
        type="button"
        onClick={() => run.mutate()}
        disabled={run.isPending}
        className="mt-2 rounded border border-gray-300 px-3 py-1 text-sm hover:bg-gray-50 disabled:opacity-50"
      >
        <Trans>Trigger refresh walk</Trans>
      </button>
      {run.isSuccess && <p className="mt-2 text-sm text-gray-700"><Trans>Walk started.</Trans></p>}
      {run.isError && (
        <p role="alert" className="mt-2 text-sm text-red-700">
          {refreshErrorMessage(run.error, i18n)}
        </p>
      )}
    </section>
  )
}
