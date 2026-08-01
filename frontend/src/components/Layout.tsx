import { Trans } from '@lingui/react/macro'
import { useQuery } from '@tanstack/react-query'
import { useEffect } from 'react'
import { Navigate, Outlet, useLocation, useNavigate } from 'react-router'
import { ApiError, fetchMe } from '../api/client'
import AppBar from './AppBar'
import Footer from './Footer'

// Layout is the authenticated shell: it gates on /api/me (401 bounces
// to login), then renders the app bar and the routed page. Every
// signed-in page nests under it.
export default function Layout() {
  const me = useQuery({ queryKey: ['me'], queryFn: fetchMe })
  const navigate = useNavigate()
  const location = useLocation()

  // Login stashes an intended destination to sessionStorage before an
  // OAuth round trip (the gateway always redirects back to /, so the
  // SPA has to re-apply it). Consumed once, here, right after the
  // profile that proves the session landed resolves.
  useEffect(() => {
    if (!me.data) return
    const stashed = sessionStorage.getItem('vg_next')
    if (stashed) {
      sessionStorage.removeItem('vg_next')
      void navigate(stashed, { replace: true })
    }
  }, [me.data, navigate])

  if (me.isPending) return <main className="p-8"><Trans>Loading...</Trans></main>
  if (me.isError) {
    if (me.error instanceof ApiError && me.error.status === 401) {
      const next =
        location.pathname === '/'
          ? ''
          : `?next=${encodeURIComponent(location.pathname + location.search)}`
      return <Navigate to={`/login${next}`} replace />
    }
    return (
      <main className="p-8" role="alert">
        <Trans>Something went wrong. Please try again.</Trans>
      </main>
    )
  }

  return (
    <div className="mx-auto max-w-6xl p-4">
      <AppBar me={me.data} />
      <Outlet />
      <Footer showHelp />
    </div>
  )
}
