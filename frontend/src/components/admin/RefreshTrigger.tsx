import { Trans, useLingui } from '@lingui/react/macro'
import { t } from '@lingui/core/macro'
import type { I18n } from '@lingui/core'
import { useMutation } from '@tanstack/react-query'
import { triggerRefresh } from '../../api/admin'
import { ApiError } from '../../api/client'
import LeverCard from './LeverCard'

// t(i18n) throughout this file, component included: refreshErrorMessage
// is a plain function (cannot call useLingui() itself, same reasoning as
// rowMeta.tsx), so it takes the caller's i18n explicitly. The component
// below uses the identical explicit form for its own strings rather than
// importing a second, same-named t from the react macro.
function refreshErrorMessage(e: unknown, i18n: I18n): string {
  if (e instanceof ApiError && e.code === 'refresh_in_progress') return t(i18n)`A refresh is already running.`
  return t(i18n)`The trigger failed - try again.`
}

// RefreshTrigger fires the same catalog refresh the nightly CronJob runs;
// the 202 comes back immediately and the refresh proceeds detached.
export default function RefreshTrigger() {
  const { i18n } = useLingui()
  const run = useMutation({ mutationFn: triggerRefresh })
  return (
    <LeverCard
      title={t(i18n)`Catalog refresh`}
      actionLabel={t(i18n)`Trigger catalog refresh`}
      onRun={() => run.mutate()}
      pending={run.isPending}
      success={run.isSuccess ? <Trans>Refresh started.</Trans> : undefined}
      error={run.isError ? refreshErrorMessage(run.error, i18n) : undefined}
    />
  )
}
