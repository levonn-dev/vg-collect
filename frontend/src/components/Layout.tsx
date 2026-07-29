import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { useEffect } from 'react'
import { Navigate, NavLink, Outlet, useLocation, useNavigate } from 'react-router'
import { ApiError, fetchMe, logout } from '../api/client'
import Avatar from './Avatar'
import CurrencySelect from './CurrencySelect'
import Logo from './Logo'
import ThemeToggle from './ThemeToggle'

function navClass({ isActive }: { isActive: boolean }): string {
  return isActive
    ? 'text-sm font-semibold text-gray-900'
    : 'text-sm text-gray-500 hover:text-gray-900'
}

// Layout is the authenticated shell: it gates on /api/me (401 bounces
// to login), then renders the primary nav, the user menu, and the
// routed page. Every signed-in page nests under it.
export default function Layout() {
  const me = useQuery({ queryKey: ['me'], queryFn: fetchMe })
  const queryClient = useQueryClient()
  const navigate = useNavigate()
  const location = useLocation()
  const signOut = useMutation({
    mutationFn: logout,
    // onSettled fires on both an HTTP error and a network failure:
    // either way the session is gone or unreachable, so clear the
    // cache and navigate away unconditionally.
    onSettled: () => {
      queryClient.clear()
      void navigate('/login')
    },
  })

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

  if (me.isPending) return <main className="p-8">Loading...</main>
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
        Something went wrong. Please try again.
      </main>
    )
  }

  return (
    <div className="mx-auto max-w-6xl p-4">
      <header
        className="flex items-center justify-between border-b border-gray-200 pb-3"
        aria-label="App bar"
      >
        <div className="flex items-baseline gap-6">
          <div className="flex items-center gap-2">
            <Logo />
            <h1 className="text-xl font-bold">vgkeep</h1>
          </div>
          <nav className="flex gap-4" aria-label="Primary">
            <NavLink to="/collection" end className={navClass}>
              Collection
            </NavLink>
            <NavLink to="/add" className={navClass}>
              Add
            </NavLink>
            <NavLink to="/explore" className={navClass}>
              Explore
            </NavLink>
            <NavLink to="/feed" className={navClass}>
              Feed
            </NavLink>
            <NavLink to="/recommendations" className={navClass}>
              Recommendations
            </NavLink>
            {me.data.roles.includes('admin') && (
              <NavLink to="/admin" className={navClass}>
                Admin
              </NavLink>
            )}
          </nav>
        </div>
        <div className="flex items-center gap-3">
          <CurrencySelect />
          <ThemeToggle />
          <NavLink
            to="/account"
            aria-label="Account"
            className="flex items-center gap-3 rounded px-1 py-0.5 hover:bg-gray-50"
          >
            <Avatar key={me.data.avatar_url} url={me.data.avatar_url} label={me.data.handle} size="md" />
            <span className="text-sm text-gray-700">@{me.data.handle}</span>
          </NavLink>
          <button
            onClick={() => signOut.mutate()}
            disabled={signOut.isPending}
            className="rounded border border-gray-300 px-3 py-1 text-sm hover:bg-gray-50 disabled:opacity-50"
          >
            Log out
          </button>
        </div>
      </header>
      <Outlet />
    </div>
  )
}
