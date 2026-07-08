import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { useState } from 'react'
import { Navigate, NavLink, Outlet, useNavigate } from 'react-router'
import { ApiError, fetchMe, logout } from '../api/client'

function navClass({ isActive }: { isActive: boolean }): string {
  return isActive
    ? 'text-sm font-semibold text-gray-900'
    : 'text-sm text-gray-500 hover:text-gray-900'
}

// Avatar renders the provider profile image with a same-size initial
// fallback: third-party avatar hosts flake (aborted first loads,
// referrer-sensitive throttling), and a failed <img> never retries on
// its own, so a failure must degrade to something stable instead of a
// stuck blank. no-referrer sidesteps googleusercontent's
// referrer-based rejections. Callers key the element by the URL so a
// changed avatar remounts with a fresh attempt.
function Avatar({ url, name }: { url?: string; name: string }) {
  const [failed, setFailed] = useState(false)
  if (!url || failed) {
    return (
      <span
        aria-hidden="true"
        className="flex h-8 w-8 items-center justify-center rounded-full bg-gray-200 text-sm font-bold text-gray-500"
      >
        {name.charAt(0)}
      </span>
    )
  }
  return (
    <img
      src={url}
      alt=""
      referrerPolicy="no-referrer"
      onError={() => setFailed(true)}
      className="h-8 w-8 rounded-full"
    />
  )
}

// Layout is the authenticated shell: it gates on /api/me (401 bounces
// to login), then renders the primary nav, the user menu, and the
// routed page. Every signed-in page nests under it.
export default function Layout() {
  const me = useQuery({ queryKey: ['me'], queryFn: fetchMe })
  const queryClient = useQueryClient()
  const navigate = useNavigate()
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

  if (me.isPending) return <main className="p-8">Loading...</main>
  if (me.isError) {
    if (me.error instanceof ApiError && me.error.status === 401) {
      return <Navigate to="/login" replace />
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
          <h1 className="text-xl font-bold">vg-collect</h1>
          <nav className="flex gap-4" aria-label="Primary">
            <NavLink to="/" end className={navClass}>
              Collection
            </NavLink>
            <NavLink to="/add" className={navClass}>
              Add
            </NavLink>
            <NavLink to="/dashboard" className={navClass}>
              Dashboard
            </NavLink>
            <NavLink to="/recommendations" className={navClass}>
              Recommendations
            </NavLink>
          </nav>
        </div>
        <div className="flex items-center gap-3">
          <Avatar key={me.data.avatar_url} url={me.data.avatar_url} name={me.data.display_name} />
          <span className="text-sm text-gray-700">{me.data.display_name}</span>
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
