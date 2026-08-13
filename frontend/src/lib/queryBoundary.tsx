import type { ReactNode } from 'react'

export type QueryBoundarySize = 'page' | 'subsection'

interface QueryLike {
  isPending: boolean
  isError: boolean
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

// renderQueryState is the shared isPending/isError gate every page,
// subsection, and admin worklist opens with. Returns undefined once
// the query is neither pending nor erroring. A plain function, not a
// component, because call sites split between early-return bodies
// (page-level: `if (x.isPending) return ...`) and inline JSX
// (`{x.isPending && ...}`), and both read naturally off one function:
//
//   - Sites whose real content reads `.data` straight off the query
//     (no `?.`) still check isPending/isError THEMSELVES first, so
//     TypeScript keeps narrowing `.data` afterward (a plain function
//     call cannot narrow the caller's own variable):
//       if (x.isPending || x.isError) return renderQueryState(x, cfg)
//       // x.data safe below
//   - Sites whose real content already derives everything through
//     `?.`/`??` (so there is nothing left to narrow) call it directly:
//       renderQueryState(x, cfg) ?? (...real content...)
export function renderQueryState(query: QueryLike, config: QueryBoundaryConfig): ReactNode | undefined {
  const { size, loading, error, role, className, notFound } = config
  if (query.isPending) {
    return size === 'page'
      ? <main className="py-8">{loading}</main>
      : <p className={subsectionClass(className, 'text-gray-500')}>{loading}</p>
  }
  if (query.isError) {
    if (notFound !== undefined) return notFound
    return size === 'page'
      ? <main className="py-8" role={role}>{error}</main>
      : (
        <p className={subsectionClass(className, role === 'alert' ? 'text-red-700' : 'text-gray-500')} role={role}>
          {error}
        </p>
      )
  }
  // Neither pending nor error: the caller renders its real content.
  // Sites that need `.data` narrowed check isPending/isError
  // themselves before calling this function (see the module comment),
  // so in practice this function is only ever invoked when one of the
  // two is already known true; this branch exists for the sites that
  // call it unconditionally via `renderQueryState(query, cfg) ?? (...)`.
  return undefined
}

function subsectionClass(className: string | undefined, colorClass: string): string {
  return `${className ? `${className} ` : ''}text-sm ${colorClass}`
}
