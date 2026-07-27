import { useEffect, useRef, useState } from 'react'

type CopyState = 'idle' | 'copied' | 'failed'

// How long the transient Copied/Copy failed text shows before the
// button reverts to its resting label.
const REVERT_MS = 2000

interface CopyButtonProps {
  text: string
  label?: string
  className?: string
}

// CopyButton is the shared copy-to-clipboard control (ShelfManager's
// per-shelf link, Account's profile link): a click copies `text` and
// the button's own visible text swaps to Copied - or Copy failed, on
// a rejected clipboard write, with no unhandled rejection either way
// - for REVERT_MS before reverting. The button's accessible name
// stays fixed at `label` throughout (aria-label, not its swapping
// text content), so callers can hold or re-query the element by one
// stable name across a click. The transient state is announced
// instead through a visually-hidden role="status" SIBLING, not a
// child - nesting a live region inside a button fights the button's
// own children-are-presentational semantics across screen readers.
// That sibling stays mounted for the whole lifetime of the button
// (rather than inserted only while transient, the way Account's own
// "Saved." confirmation works) so a screen reader has one stable live
// region to read the change from. Re-clicking while still within an
// earlier window restarts the revert timer cleanly instead of letting
// an earlier one revert the state out from under the new click:
// settle() always clears whatever timer is pending before scheduling
// its own, whether that timer came from this same click's copy() or
// from an EARLIER click that is still in flight when this one
// settles. A mounted flag (also flipped in the unmount cleanup below)
// keeps a clipboard promise that resolves after unmount from touching
// state or scheduling a timer nothing would ever clear.
export default function CopyButton({ text, label = 'Copy link', className = '' }: CopyButtonProps) {
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

  const shown = state === 'copied' ? 'Copied' : state === 'failed' ? 'Copy failed' : label
  const announced = state === 'idle' ? '' : shown
  return (
    <>
      <button
        type="button"
        onClick={copy}
        aria-label={label}
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
