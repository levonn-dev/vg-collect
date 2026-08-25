import { useEffect } from 'react'

const SUFFIX = ' - vgkeep'

// Sets the tab title while mounted (WCAG 2.4.2: routes need a distinct
// title), restores the prior title on unmount.
export function useDocumentTitle(title: string) {
  useEffect(() => {
    const prev = document.title
    document.title = `${title}${SUFFIX}`
    return () => { document.title = prev }
  }, [title])
}
