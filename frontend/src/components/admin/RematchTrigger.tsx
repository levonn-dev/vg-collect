import { Trans, useLingui } from '@lingui/react/macro'
import { t } from '@lingui/core/macro'
import type { I18n } from '@lingui/core'
import { useMutation } from '@tanstack/react-query'
import { triggerRematch } from '../../api/admin'
import { ApiError } from '../../api/client'
import LeverCard from './LeverCard'

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
    <LeverCard
      title={t(i18n)`Entry rematch`}
      actionLabel={t(i18n)`Trigger entry rematch`}
      onRun={() => run.mutate()}
      pending={run.isPending}
      success={run.isSuccess ? <Trans>Rematch started.</Trans> : undefined}
      error={run.isError ? rematchErrorMessage(run.error, i18n) : undefined}
    />
  )
}
