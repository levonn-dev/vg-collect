import { Component } from 'react'
import type { ReactNode } from 'react'
import { recordUncaughtError } from '../telemetry'

interface ErrorBoundaryProps {
  children: ReactNode
}

interface ErrorBoundaryState {
  crashed: boolean
}

// Two deliberate exceptions to how the rest of this app is built:
//
// 1. The fallback below is hard-coded English, not <Trans>/t/msg. A
//    render crash can originate inside the i18n runtime itself (a bad
//    catalog, a broken provider); a fallback that depended on that
//    same runtime could fail to render right when it matters most.
//
// 2. componentDidCatch records kind=boundary with no double-count
//    guard against the window 'error' listener in telemetry.ts:
//    React routes boundary-caught errors through the root's
//    onCaughtError (default: console.error only); only errors no
//    boundary catches dispatch the window ErrorEvent that listener
//    sees. The two counts are disjoint by construction.
export default class ErrorBoundary extends Component<ErrorBoundaryProps, ErrorBoundaryState> {
  state: ErrorBoundaryState = { crashed: false }

  static getDerivedStateFromError(): ErrorBoundaryState {
    return { crashed: true }
  }

  // No error message or stack recorded here - cardinality (free-text
  // messages as a metric attribute), and the trace pipeline already
  // carries that detail for whichever request was in flight, same
  // reasoning as the window listeners in telemetry.ts.
  componentDidCatch(): void {
    recordUncaughtError('boundary')
  }

  render() {
    if (!this.state.crashed) return this.props.children
    return (
      <main aria-label="Application error" className="p-6">
        <h2 className="mb-2 text-2xl font-bold">Something went wrong</h2>
        <p role="alert" className="text-sm text-gray-600">
          The page hit an error it could not recover from. Reloading usually fixes it.
        </p>
        <button
          type="button"
          onClick={() => window.location.reload()}
          className="mt-4 rounded border border-gray-300 px-2 py-1 text-sm text-gray-600 hover:bg-gray-50"
        >
          Reload
        </button>
      </main>
    )
  }
}
