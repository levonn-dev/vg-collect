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
  // Every caller's success text differs in shape: Normalize's is derived
  // from the response counts, Refresh/Rematch's is a fixed sentence that
  // ignores the response body entirely. A required renderer covers both
  // without a default that would have to guess the response shape.
  successMessage: (data: T | undefined) => ReactNode
  // inProgressCode/inProgressMessage cover the one ApiError code each
  // trigger treats specially (a 409 for a sweep that is already
  // running); Normalize's three callers have no such code and omit
  // both. failureMessage is the generic fallback for every other
  // error, and its wording differs per caller, so it stays a prop
  // rather than a second hardcoded string.
  inProgressCode?: string
  inProgressMessage?: ReactNode
  failureMessage?: ReactNode
}

// The scanned/normalized/skipped sentence Admin's three normalize-*
// triggers share; exported so Admin.tsx passes the same function three
// times instead of repeating the sentence three times.
// eslint-disable-next-line react-refresh/only-export-components -- shared with Admin.tsx's three normalize-* triggers, alongside this component.
export function normalizeSuccessMessage(data: NormalizeResult | undefined): ReactNode {
  const scanned = data?.scanned ?? 0
  const normalized = data?.normalized ?? 0
  const skipped = data?.skipped ?? 0
  return <Trans>Scanned {scanned}, promoted {normalized}, skipped {skipped}.</Trans>
}

// NormalizeTrigger is the generic admin trigger lever: one mutation, a
// success line, and an error line with an optional conflict-specific
// message. Shared by Admin's three normalize levers (platforms,
// regions, community regions) and the catalog-refresh and
// entry-rematch levers, so each caller configures
// it directly instead of wrapping its own near-identical component.
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
