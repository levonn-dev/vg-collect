import { useLingui } from '@lingui/react/macro'
import { useEffect, useRef, useState } from 'react'

type CopyState = 'idle' | 'copied' | 'failed'

// How long the transient Copied/Copy failed text shows before reverting.
const REVERT_MS = 2000

interface CopyButtonProps {
  text: string
  label?: string
  className?: string
}

// aria-label stays fixed at label so callers query one stable name across clicks.
// role=status is a sibling: nesting inside the button breaks its semantics for
// screen readers, so it stays mounted permanently as the one live region.
// mounted guards a clipboard promise settling after unmount from touching state.
export default function CopyButton({ text, label, className = '' }: CopyButtonProps) {
  const { t } = useLingui()
  const resolvedLabel = label ?? t`Copy link`
  const [state, setState] = useState<CopyState>('idle')
  const revertTimer = useRef<ReturnType<typeof setTimeout> | undefined>(undefined)
  const mounted = useRef(true)

  useEffect(
    () => () => {
      mounted.current = false
      clearTimeout(revertTimer.current)
    },
    [],
  )

  const settle = (next: CopyState) => {
    clearTimeout(revertTimer.current)
    if (!mounted.current) return
    setState(next)
    revertTimer.current = setTimeout(() => setState('idle'), REVERT_MS)
  }

  const copy = () => {
    clearTimeout(revertTimer.current)
    navigator.clipboard.writeText(text).then(
      () => settle('copied'),
      () => settle('failed'),
    )
  }

  const shown = state === 'copied' ? t`Copied` : state === 'failed' ? t`Copy failed` : resolvedLabel
  const announced = state === 'idle' ? '' : shown
  return (
    <>
      <button
        type="button"
        onClick={copy}
        aria-label={resolvedLabel}
        className={`rounded border border-gray-300 hover:bg-gray-50 ${className}`.trim()}
      >
        {shown}
      </button>
      <span role="status" className="sr-only">
        {announced}
      </span>
    </>
  )
}
