import { Trans } from '@lingui/react/macro'

// First focusable element in both shells; off-screen until focused, then
// jumps to #main-content (WCAG 2.4.1 Bypass Blocks).
export default function SkipLink() {
  return (
    <a
      href="#main-content"
      className="sr-only focus:not-sr-only focus:absolute focus:top-2 focus:left-2 focus:z-50 focus:rounded focus:bg-white focus:px-3 focus:py-2 focus:text-sm focus:font-medium focus:text-gray-900 focus:shadow"
    >
      <Trans>Skip to content</Trans>
    </a>
  )
}
