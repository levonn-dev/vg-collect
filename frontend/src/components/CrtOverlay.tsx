import { useState } from 'react'
import { useKonami } from '../lib/useKonami'

// Konami-code easter egg: a static scanline-and-vignette wash over the whole
// app until the code is entered again. Purely visual, so it stays out of the
// a11y tree; not persisted across reloads.
export default function CrtOverlay() {
  const [on, setOn] = useState(false)
  useKonami(() => setOn((v) => !v))
  if (!on) return null
  return (
    <div
      data-testid="crt-overlay"
      aria-hidden="true"
      className="pointer-events-none fixed inset-0 z-50"
      style={{
        backgroundImage:
          'repeating-linear-gradient(to bottom, rgba(0, 0, 0, 0.18) 0px, rgba(0, 0, 0, 0.18) 1px, transparent 1px, transparent 3px),' +
          ' radial-gradient(ellipse at center, transparent 60%, rgba(15, 10, 40, 0.35) 100%)',
      }}
    />
  )
}
