import { useLingui } from '@lingui/react/macro'
import { useEffect, useState } from 'react'

// The inline script in index.html applies the initial theme class
// before first paint (stored choice, else system preference, else
// dark). This control flips and persists it; until the user chooses,
// live system-preference changes keep being followed.
export default function ThemeToggle() {
  const { t } = useLingui()
  const [dark, setDark] = useState(() => document.documentElement.classList.contains('dark'))

  useEffect(() => {
    const mq = window.matchMedia('(prefers-color-scheme: light)')
    const followSystem = () => {
      if (localStorage.getItem('theme') !== null) return
      const next = !mq.matches
      document.documentElement.classList.toggle('dark', next)
      setDark(next)
    }
    mq.addEventListener('change', followSystem)
    return () => mq.removeEventListener('change', followSystem)
  }, [])

  const flip = () => {
    const next = !dark
    document.documentElement.classList.toggle('dark', next)
    localStorage.setItem('theme', next ? 'dark' : 'light')
    setDark(next)
  }

  return (
    <button
      type="button"
      onClick={flip}
      className="rounded border border-gray-300 px-2 py-1 text-sm text-gray-600 hover:bg-gray-50"
    >
      {dark ? t`Switch to light mode` : t`Switch to dark mode`}
    </button>
  )
}
