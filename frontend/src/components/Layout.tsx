import { Trans } from '@lingui/react/macro'
import { useEffect } from 'react'
import { Navigate, Outlet, useLocation, useNavigate } from 'react-router'
import { ApiError } from '../api/client'
import { useMe } from '../lib/useMe'
import AppBar from './AppBar'
import Footer from './Footer'
import SkipLink from './SkipLink'

// Authenticated shell: gates on /api/me (401 bounces to login).
export default function Layout() {
  const me = useMe()
  const navigate = useNavigate()
  const location = useLocation()

  // vg_next: login stashes the intended destination before OAuth (gateway
  // always redirects to /); consumed once here after the session resolves.
  useEffect(() => {
    if (!me.data) return
    const stashed = sessionStorage.getItem('vg_next')
    if (stashed) {
      sessionStorage.removeItem('vg_next')
      void navigate(stashed, { replace: true })
    }
  }, [me.data, navigate])

  if (me.isPending) {
    return <main id="main-content" tabIndex={-1} className="p-8"><Trans>Loading...</Trans></main>
  }
  if (me.isError) {
    if (me.error instanceof ApiError && me.error.status === 401) {
      const next =
        location.pathname === '/'
          ? ''
          : `?next=${encodeURIComponent(location.pathname + location.search)}`
      return <Navigate to={`/login${next}`} replace />
    }
    return (
      <main id="main-content" tabIndex={-1} className="p-8">
        <p role="alert"><Trans>Something went wrong. Please try again.</Trans></p>
      </main>
    )
  }

  return (
    <div className="mx-auto max-w-6xl p-4">
      <SkipLink />
      <AppBar me={me.data} />
      <Outlet />
      <Footer showHelp />
    </div>
  )
}
