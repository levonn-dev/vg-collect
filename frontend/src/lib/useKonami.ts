import { useEffect, useRef } from 'react'

const SEQUENCE = [
  'arrowup', 'arrowup', 'arrowdown', 'arrowdown',
  'arrowleft', 'arrowright', 'arrowleft', 'arrowright',
  'b', 'a',
]

function isEditable(target: EventTarget | null) {
  return (
    target instanceof HTMLElement &&
    (target.isContentEditable ||
      ['INPUT', 'TEXTAREA', 'SELECT'].includes(target.tagName))
  )
}

// Fires onCode when the Konami code is typed anywhere outside a form field.
export function useKonami(onCode: () => void) {
  const callback = useRef(onCode)
  useEffect(() => {
    callback.current = onCode
  })
  useEffect(() => {
    let progress = 0
    const onKeyDown = (e: KeyboardEvent) => {
      if (isEditable(e.target)) return
      const key = e.key.toLowerCase()
      if (key === SEQUENCE[progress]) {
        progress++
      } else {
        // A miss can still open a fresh attempt (e.g. a third ArrowUp).
        progress = key === SEQUENCE[0] ? 1 : 0
      }
      if (progress === SEQUENCE.length) {
        progress = 0
        callback.current()
      }
    }
    window.addEventListener('keydown', onKeyDown)
    return () => window.removeEventListener('keydown', onKeyDown)
  }, [])
}
