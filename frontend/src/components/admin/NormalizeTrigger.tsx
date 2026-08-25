import { Trans } from '@lingui/react/macro'
import type { ReactNode } from 'react'
import { useMutation } from '@tanstack/react-query'
import type { NormalizeResult } from '../../api/admin'
import { ApiError } from '../../api/client'
import LeverCard from './LeverCard'

interface Props<T> {
  title: string
  actionLabel: string
  mutationFn: () => Promise<T>
  // Required, not defaulted: Normalize derives text from response counts,
  // Refresh/Rematch ignores the body entirely - no default could guess the shape.
  successMessage: (data: T | undefined) => ReactNode
  // inProgressCode/Message cover the one ApiError code a trigger treats
  // specially (409, sweep already running); Normalize's callers omit both.
  // failureMessage is the generic fallback, worded per caller.
  inProgressCode?: string
  inProgressMessage?: ReactNode
  failureMessage?: ReactNode
}

// Shared scanned/normalized/skipped sentence; exported so Admin.tsx passes
// one function instead of repeating the sentence three times.
// eslint-disable-next-line react-refresh/only-export-components -- shared with Admin.tsx's three normalize-* triggers, alongside this component.
export function normalizeSuccessMessage(data: NormalizeResult | undefined): ReactNode {
  const scanned = data?.scanned ?? 0
  const normalized = data?.normalized ?? 0
  const skipped = data?.skipped ?? 0
  return <Trans>Scanned {scanned}, promoted {normalized}, skipped {skipped}.</Trans>
}

// Generic admin trigger lever: mutation, success line, optional
// conflict-specific error. Callers configure it directly instead of wrapping it.
export default function NormalizeTrigger<T>({
  title,
  actionLabel,
  mutationFn,
  successMessage,
  inProgressCode,
  inProgressMessage,
  failureMessage,
}: Props<T>) {
  const run = useMutation({ mutationFn })
  const isInProgressConflict = inProgressCode !== undefined
    && run.error instanceof ApiError
    && run.error.code === inProgressCode
  return (
    <LeverCard
      title={title}
      actionLabel={actionLabel}
      onRun={() => run.mutate()}
      pending={run.isPending}
      success={run.isSuccess ? successMessage(run.data) : undefined}
      error={run.isError
        ? (isInProgressConflict ? inProgressMessage : (failureMessage ?? <Trans>The sweep failed - try again.</Trans>))
        : undefined}
    />
  )
}
