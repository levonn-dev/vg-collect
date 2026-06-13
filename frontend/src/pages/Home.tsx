import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { Navigate, useNavigate } from 'react-router'
import { ApiError, fetchMe, logout } from '../api/client'

export default function Home() {
  const me = useQuery({ queryKey: ['me'], queryFn: fetchMe })
  const queryClient = useQueryClient()
  const navigate = useNavigate()
  const signOut = useMutation({
    mutationFn: logout,
    // onSettled fires on both an HTTP error and a network failure
    // (fetch throws a TypeError): either way the session is gone or
    // unreachable, so clear the cache and navigate away unconditionally.
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
    <main className="mx-auto max-w-2xl p-8">
      <header className="flex items-center justify-between" aria-label="App bar">
        <h1 className="text-2xl font-bold">vg-collect</h1>
        <button
          onClick={() => signOut.mutate()}
          disabled={signOut.isPending}
          className="rounded border border-gray-300 px-3 py-1 text-sm hover:bg-gray-50 disabled:opacity-50"
        >
          Log out
        </button>
      </header>
      <section className="mt-8 flex items-center gap-4" aria-label="Profile">
        {me.data.avatar_url && (
          <img src={me.data.avatar_url} alt="" className="h-12 w-12 rounded-full" />
        )}
        <div>
          <p className="text-lg font-medium">{me.data.display_name}</p>
          <p className="text-sm text-gray-600">{me.data.email}</p>
          <p className="mt-1 text-xs text-gray-500">roles: {me.data.roles.join(', ')}</p>
        </div>
      </section>
      <p className="mt-8 text-gray-600">
        Your collection lives here once the collection service exists.
      </p>
    </main>
  )
}
