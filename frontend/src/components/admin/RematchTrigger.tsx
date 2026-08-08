import { Trans, useLingui } from '@lingui/react/macro'
import { t } from '@lingui/core/macro'
import type { I18n } from '@lingui/core'
import { useMutation } from '@tanstack/react-query'
import { triggerRematch } from '../../api/admin'
import { ApiError } from '../../api/client'

// t(i18n) throughout this file, component included: rematchErrorMessage
// is a plain function (cannot call useLingui() itself, same reasoning as
// rowMeta.tsx), so it takes the caller's i18n explicitly. The component
// below uses the identical explicit form for its own strings rather than
// importing a second, same-named t from the react macro.
function rematchErrorMessage(e: unknown, i18n: I18n): string {
  if (e instanceof ApiError && e.code === 'rematch_in_progress') return t(i18n)`A rematch is already running.`
  return t(i18n)`The rematch trigger failed - try again.`
}

// RematchTrigger fires the same entry rematch the nightly CronJob runs;
// the 202 comes back immediately and the rematch proceeds detached.
export default function RematchTrigger() {
  const { i18n } = useLingui()
  const run = useMutation({ mutationFn: triggerRematch })
  return (
    <section aria-label={t(i18n)`Entry rematch`} className="mt-6">
      <h3 className="text-base font-semibold"><Trans>Entry rematch</Trans></h3>
      <button
        type="button"
        onClick={() => run.mutate()}
        disabled={run.isPending}
        className="mt-2 rounded border border-gray-300 px-3 py-1 text-sm hover:bg-gray-50 disabled:opacity-50"
      >
        <Trans>Trigger entry rematch</Trans>
      </button>
      {run.isSuccess && <p className="mt-2 text-sm text-gray-700"><Trans>Rematch started.</Trans></p>}
      {run.isError && (
        <p role="alert" className="mt-2 text-sm text-red-700">
          {rematchErrorMessage(run.error, i18n)}
        </p>
      )}
    </section>
  )
}
