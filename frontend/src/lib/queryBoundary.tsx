import type { ReactNode } from 'react'
import { Trans } from '@lingui/react/macro'

export type QueryBoundarySize = 'page' | 'subsection'

interface QueryLike {
  isPending: boolean
  isError: boolean
  // TanStack keeps last-loaded data through a failed background
  // refetch; presence (not value) distinguishes that from a first-load
  // failure. `unknown`: only presence is ever checked.
  data: unknown
}

export interface QueryBoundaryConfig {
  size: QueryBoundarySize
  loading: ReactNode
  error: ReactNode
  // Every site alerts on real failure except PricingPanel's match-status
  // check (own call site), which omits role. Subsection error is
  // red-700 with role, gray-500 without.
  role?: 'alert'
  // subsection only. Sites under a Tabs control or worklist heading add
  // their own top margin; others pass nothing.
  className?: string
  // Replaces the generic error output (distinct surface or same
  // wrapper, different copy). Caller resolves the condition (typically
  // a 404) before passing it.
  notFound?: ReactNode
}

// Shared isPending/first-load-error gate; undefined once real content
// should render (success, or a backgrounded error still holding data -
// that's refetchWarning below, not this). A plain function so it fits
// both early-return and inline `?? (...)` call sites without narrowing loss.
export function renderQueryState(query: QueryLike, config: QueryBoundaryConfig): ReactNode | undefined {
  const { size, loading, error, role, className, notFound } = config
  if (query.isPending) {
    return size === 'page'
      ? <main id="main-content" tabIndex={-1} className="py-8">{loading}</main>
      : <p className={subsectionClass(className, 'text-gray-500')}>{loading}</p>
  }
  // First-load failure (no prior data) is the only case replacing
  // everything; error-with-data falls through like a clean success.
  if (query.isError && query.data === undefined) {
    if (notFound !== undefined) return notFound
    return size === 'page'
      // role never on <main> itself: it would override the implicit landmark role.
      ? <main id="main-content" tabIndex={-1} className="py-8"><div role={role}>{error}</div></main>
      : (
        <p className={subsectionClass(className, role === 'alert' ? 'text-red-700' : 'text-gray-500')} role={role}>
          {error}
        </p>
      )
  }
  // Real content renders here (success or backgrounded error); this
  // branch exists for unconditional `?? (...)` call sites.
  return undefined
}

// Companion to renderQueryState for the isError-with-data case: a
// non-blocking notice rendered alongside real content, never in place
// of it. role="status", not "alert": a backgrounded failure with good
// data on screen isn't urgent; never a landmark, so page structure
// never shifts under assistive tech.
export function refetchWarning(query: QueryLike): ReactNode | undefined {
  if (!query.isError || query.data === undefined) return undefined
  return (
    <p role="status" className="mb-2 text-sm text-amber-800">
      <Trans>The last refresh failed - showing what was loaded before.</Trans>
    </p>
  )
}

function subsectionClass(className: string | undefined, colorClass: string): string {
  return `${className ? `${className} ` : ''}text-sm ${colorClass}`
}
