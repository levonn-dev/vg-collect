import { Trans } from '@lingui/react/macro'
import type { ReactNode } from 'react'

type NoticeTone = 'green' | 'amber'

// Full class strings per tone, not interpolated: Tailwind only picks up
// literal class strings, so bg-${tone}-50 would silently drop from the build.
const TONES: Record<NoticeTone, { container: string; button: string }> = {
  green: {
    container: 'mb-4 flex items-start justify-between gap-3 rounded bg-green-50 p-3 text-sm text-green-800',
    button: 'shrink-0 rounded border border-green-300 px-2 py-0.5 hover:bg-white',
  },
  amber: {
    container: 'mb-4 flex items-start justify-between gap-3 rounded bg-amber-50 p-3 text-sm text-amber-800',
    button: 'shrink-0 rounded border border-amber-300 px-2 py-0.5 hover:bg-white',
  },
}

interface DismissibleNoticeProps {
  tone: NoticeTone
  dismissLabel: string
  onDismiss: () => void
  children: ReactNode
}

// Banner shell ApprovalNotice/RegionMismatchBanner render around
// useDismissibleAck's state: a role=status bar with a message and Dismiss.
export default function DismissibleNotice({ tone, dismissLabel, onDismiss, children }: DismissibleNoticeProps) {
  const cls = TONES[tone]
  return (
    <div role="status" className={cls.container}>
      <p>{children}</p>
      <button type="button" aria-label={dismissLabel} onClick={onDismiss} className={cls.button}>
        <Trans>Dismiss</Trans>
      </button>
    </div>
  )
}
