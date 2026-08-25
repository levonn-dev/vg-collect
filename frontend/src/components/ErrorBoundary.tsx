import { Component } from 'react'
import type { ReactNode } from 'react'
import { recordUncaughtError } from '../telemetry'

interface ErrorBoundaryProps {
  children: ReactNode
}

interface ErrorBoundaryState {
  crashed: boolean
}

// Fallback text is hard-coded English, not i18n: a crash can originate inside
// the i18n runtime itself.
// componentDidCatch's boundary count can't double-count telemetry.ts's window
// 'error' listener: React fires onCaughtError, not a window ErrorEvent, for
// caught errors.
export default class ErrorBoundary extends Component<ErrorBoundaryProps, ErrorBoundaryState> {
  state: ErrorBoundaryState = { crashed: false }

  static getDerivedStateFromError(): ErrorBoundaryState {
    return { crashed: true }
  }

  // No message or stack recorded: cardinality risk as a metric attribute, and
  // the trace pipeline already carries that detail.
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
