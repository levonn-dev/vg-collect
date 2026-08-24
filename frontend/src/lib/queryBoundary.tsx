import type { ReactNode } from 'react'
import { Trans } from '@lingui/react/macro'

export type QueryBoundarySize = 'page' | 'subsection'

interface QueryLike {
  isPending: boolean
  isError: boolean
  // Presence of data from a prior successful fetch, independent of
  // isError: TanStack keeps the last-loaded value in place through a
  // failed background refetch (isError true, data still defined), so
  // this is what tells that case apart from a first-load failure
  // (isError true, data undefined). `unknown`, not a generic - every
  // caller's own query result satisfies this structurally, and this
  // file only ever checks presence, never the value itself.
  data: unknown
}

export interface QueryBoundaryConfig {
  size: QueryBoundarySize
  loading: ReactNode
  error: ReactNode
  // Every site alerts assistive tech on a real failure; PricingPanel's
  // inline match-status check is the one deliberate exception (see its
  // call site) and omits role so a still-checking/unavailable note
  // never interrupts a screen reader. Subsection error text follows
  // suit: red-700 with role, or the same muted gray-500 as loading
  // when role is left off.
  role?: 'alert'
  // subsection only. Sites stacked directly under a Tabs control or a
  // worklist heading add their own top margin (mt-4 / mt-3 / mt-2);
  // CommentList and SharedShelf's entries section sit right under an
  // element that already spaces them and pass nothing.
  className?: string
  // Fully replaces the generic error output once the query errors - a
  // distinct surface (NotFoundState) or the same wrapper with
  // different copy (EntryDetail's inline "does not exist"). Callers
  // resolve their own condition (typically a 404 ApiError) before
  // passing it; leave undefined for the generic error.
  notFound?: ReactNode
}

// renderQueryState is the shared isPending/first-load-error gate every
// page, subsection, and admin worklist opens with. Returns undefined
// once the query has real content to show - either a clean success, or
// an error on a background refetch that still holds data from a prior
// fetch (that case gets refetchWarning below, not this function's hard
// error UI, so the caller's own good, currently-displayed content
// keeps rendering instead of being discarded for a failure that never
// touched it). A plain function, not a component, because call sites
// split between early-return bodies (page-level: `if (x.isPending)
// return ...`) and inline JSX (`{x.isPending && ...}`), and both read
// naturally off one function:
//
//   - Sites whose real content reads `.data` straight off the query
//     (no `?.`) still check isPending/isError-with-no-data THEMSELVES
//     first, so TypeScript keeps narrowing `.data` afterward (a plain
//     function call cannot narrow the caller's own variable):
//       if (x.isPending || (x.isError && x.data === undefined)) return renderQueryState(x, cfg)
//       // x.data safe below - includes the good-data-despite-isError case
//   - Sites whose real content already derives everything through
//     `?.`/`??` (so there is nothing left to narrow) call it directly:
//       renderQueryState(x, cfg) ?? (...real content...)
//     These need no condition change at all: this function already
//     returns undefined for the isError-with-data case, so `??` falls
//     through to the real content on its own.
//
// Either shape still calls refetchWarning (below) itself, separately,
// wherever the real content renders - this function's own return is
// reserved for "replace everything," never "augment it."
export function renderQueryState(query: QueryLike, config: QueryBoundaryConfig): ReactNode | undefined {
  const { size, loading, error, role, className, notFound } = config
  if (query.isPending) {
    return size === 'page'
      ? <main className="py-8">{loading}</main>
      : <p className={subsectionClass(className, 'text-gray-500')}>{loading}</p>
  }
  // A first-load failure (no prior data to fall back on) is the only
  // case that still replaces everything with the hard error UI. An
  // error with data present falls through to the final `return
  // undefined` below, same as a clean success.
  if (query.isError && query.data === undefined) {
    if (notFound !== undefined) return notFound
    return size === 'page'
      // role never sits on <main> itself: an explicit role overrides an
      // element's implicit ARIA role, so <main role="alert"> would stop
      // being exposed as the page's landmark while the error shows.
      ? <main className="py-8"><div role={role}>{error}</div></main>
      : (
        <p className={subsectionClass(className, role === 'alert' ? 'text-red-700' : 'text-gray-500')} role={role}>
          {error}
        </p>
      )
  }
  // Neither pending nor a first-load error: the caller renders its
  // real content, whether that is a clean success or an error on a
  // background refetch that still holds prior data (refetchWarning
  // covers that case's notice). Sites that need `.data` narrowed check
  // isPending/isError-with-no-data themselves before calling this
  // function (see the module comment), so in practice this function is
  // only ever invoked when one of those is already known true; this
  // branch exists for the sites that call it unconditionally via
  // `renderQueryState(query, cfg) ?? (...)`.
  return undefined
}

// refetchWarning is renderQueryState's companion for the
// isError-with-data case: a small, non-blocking notice callers render
// inline alongside their real content (never in place of it - see
// renderQueryState's own doc comment on why the two are separate
// functions). role="status", not "alert": a background refetch failure
// that still left good data on screen is not urgent enough to
// interrupt a screen reader the way a first-load error is. Always a
// bare inline element, never a landmark of its own, so a page's
// landmark structure never shifts under an assistive-tech user just
// because a background refresh failed.
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
