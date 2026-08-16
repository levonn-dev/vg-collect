import { Trans, useLingui } from '@lingui/react/macro'
import { msg } from '@lingui/core/macro'
import type { MessageDescriptor } from '@lingui/core'
import { useQuery } from '@tanstack/react-query'
import { Navigate, useSearchParams } from 'react-router'
import { fetchProviders } from '../api/client'
import SectionLabel from '../components/SectionLabel'
import { devFixtures, providerNames } from '../lib/providers'
import { useMe } from '../lib/useMe'

const errorMessages: Record<string, MessageDescriptor> = {
  login_failed: msg`Login failed. Please try again.`,
  email_unverified: msg`That account has no verified email address; verify it there and sign in again.`,
  provider_error: msg`That sign-in service is unavailable. Please try again shortly.`,
}

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
  const { t, i18n } = useLingui()
  const [params] = useSearchParams()
  const me = useMe()
  const providers = useQuery({ queryKey: ['providers'], queryFn: fetchProviders })
  const error = params.get('error')
  const next = safeNext(params.get('next'))
  const stash = () => {
    if (next) sessionStorage.setItem('vg_next', next)
  }

  // A live session has no business here (the browser's back button
  // lands on this page after the OAuth hop, since that hop is a full
  // navigation): bounce to the requested page or home.
  if (me.data) return <Navigate to={next ?? '/'} replace />

  return (
    <main
      aria-label={t`Sign in`}
      className="mx-auto flex w-full max-w-sm flex-1 flex-col justify-center gap-6 p-6"
    >
      <p className="text-gray-600"><Trans>Track your game collection.</Trans></p>
      {error && (
        <p role="alert" className="rounded bg-red-50 p-3 text-sm text-red-700">
          {i18n._(errorMessages[error] ?? errorMessages.login_failed)}
        </p>
      )}
      {params.get('deleted') && (
        <p role="status" className="rounded bg-green-50 p-3 text-sm text-green-800">
          <Trans>Your account has been deleted.</Trans>
        </p>
      )}
      {providers.isPending && <p><Trans>Loading sign-in options...</Trans></p>}
      {providers.isError && (
        <p role="alert" className="rounded bg-red-50 p-3 text-sm text-red-700">
          <Trans>Sign-in is unavailable right now.</Trans>
        </p>
      )}
      <div className="flex flex-col gap-3">
        {providers.data
          ?.filter((p) => p !== 'dev')
          .map((p) => {
            const providerName = providerNames[p] ?? p
            return (
              <a
                key={p}
                href={`/api/auth/login?provider=${p}`}
                onClick={stash}
                className="rounded border border-gray-300 px-4 py-2 text-center font-medium hover:bg-gray-50"
              >
                <Trans>Continue with {providerName}</Trans>
              </a>
            )
          })}
        {providers.data?.includes('dev') && (
          <div className="mt-2 border-t border-gray-200 pt-4">
            <SectionLabel as="p" size="xs" bold={false} className="mb-2"><Trans>Dev fixtures</Trans></SectionLabel>
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
