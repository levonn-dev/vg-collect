import { useQuery } from '@tanstack/react-query'
import { useSearchParams } from 'react-router'
import { fetchProviders } from '../api/client'

const errorMessages: Record<string, string> = {
  login_failed: 'Login failed. Please try again.',
  email_unverified: 'That account has no verified email address; verify it there and sign in again.',
  provider_error: 'That sign-in service is unavailable. Please try again shortly.',
}

const providerLabels: Record<string, string> = {
  google: 'Continue with Google',
  twitch: 'Continue with Twitch',
}

const devFixtures = ['alice', 'bob', 'admin']

// next must be an internal path: leading slash, not protocol-relative.
function safeNext(raw: string | null): string | null {
  if (!raw || !raw.startsWith('/') || raw.startsWith('//')) return null
  return raw
}

// Login links are full navigations on purpose: the OAuth dance needs
// the browser to follow the gateway's redirects, and the dev provider
// sets the session cookie on the same hop. The gateway always lands
// back on /, so a requested destination is stashed to sessionStorage
// before the hop and Layout re-applies it once the session resolves.
export default function Login() {
  const [params] = useSearchParams()
  const providers = useQuery({ queryKey: ['providers'], queryFn: fetchProviders })
  const error = params.get('error')
  const next = safeNext(params.get('next'))
  const stash = () => {
    if (next) sessionStorage.setItem('vg_next', next)
  }

  return (
    <main
      aria-label="Sign in"
      className="mx-auto flex w-full max-w-sm flex-1 flex-col justify-center gap-6 p-6"
    >
      <p className="text-gray-600">Track your game collection.</p>
      {error && (
        <p role="alert" className="rounded bg-red-50 p-3 text-sm text-red-700">
          {errorMessages[error] ?? errorMessages.login_failed}
        </p>
      )}
      {params.get('deleted') && (
        <p role="status" className="rounded bg-green-50 p-3 text-sm text-green-800">
          Your account has been deleted.
        </p>
      )}
      {providers.isPending && <p>Loading sign-in options...</p>}
      {providers.isError && (
        <p role="alert" className="rounded bg-red-50 p-3 text-sm text-red-700">
          Sign-in is unavailable right now.
        </p>
      )}
      <div className="flex flex-col gap-3">
        {providers.data
          ?.filter((p) => p !== 'dev')
          .map((p) => (
            <a
              key={p}
              href={`/api/auth/login?provider=${p}`}
              onClick={stash}
              className="rounded border border-gray-300 px-4 py-2 text-center font-medium hover:bg-gray-50"
            >
              {providerLabels[p] ?? `Continue with ${p}`}
            </a>
          ))}
        {providers.data?.includes('dev') && (
          <div className="mt-2 border-t border-gray-200 pt-4">
            <p className="mb-2 text-xs uppercase tracking-wide text-gray-500">Dev fixtures</p>
            <div className="flex gap-2">
              {devFixtures.map((user) => (
                <a
                  key={user}
                  href={`/api/auth/login?provider=dev&user=${user}`}
                  onClick={stash}
                  className="flex-1 rounded bg-gray-900 px-3 py-2 text-center text-sm text-white hover:bg-gray-700"
                >
                  {user}
                </a>
              ))}
            </div>
          </div>
        )}
      </div>
    </main>
  )
}
