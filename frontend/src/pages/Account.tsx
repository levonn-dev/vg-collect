import { Trans, useLingui } from '@lingui/react/macro'
import { msg, t } from '@lingui/core/macro'
import type { MessageDescriptor } from '@lingui/core'
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { useNavigate, useSearchParams } from 'react-router'
import { deleteAccount, fetchIdentities, fetchProviders } from '../api/me'
import LinkedLogins from '../components/account/LinkedLogins'
import ProfileForm from '../components/account/ProfileForm'
import SectionLabel from '../components/SectionLabel'
import { confirmThen } from '../lib/confirm'
import { btnPrimary } from '../lib/formStyles'
import { devFixtures, providerNames } from '../lib/providers'
import { useMe } from '../lib/useMe'

const linkErrorMessages: Record<string, MessageDescriptor> = {
  conflict: msg`That login already belongs to another account, so it was not linked.`,
  email_unverified: msg`That login has no verified email address; verify it there and try again.`,
  link_failed: msg`Linking failed. Please try again.`,
}

// Account is the self-service page: profile fields, linked provider
// logins (link, unlink), and account deletion. Link buttons are full
// navigations like login: the OAuth dance needs real redirects.
export default function Account() {
  const { i18n } = useLingui()
  const [params, setParams] = useSearchParams()
  const navigate = useNavigate()
  const queryClient = useQueryClient()
  const me = useMe()
  const identities = useQuery({ queryKey: ['identities'], queryFn: fetchIdentities })
  const providers = useQuery({ queryKey: ['providers'], queryFn: fetchProviders })
  const removeAccount = useMutation({
    mutationFn: deleteAccount,
    onSuccess: () => {
      queryClient.clear()
      void navigate('/login?deleted=1')
    },
  })

  const linked = params.get('linked')
  const linkError = params.get('link_error')
  const email = me.data?.email

  return (
    <main className="flex flex-col gap-8 py-6">
      <h2 className="text-2xl font-bold"><Trans>Account</Trans></h2>

      {linked && (
        <p role="status" className="max-w-md rounded bg-green-50 p-3 text-sm text-green-800">
          <Trans>Login linked. Signing in with it now lands in this account.</Trans>
          <button type="button" className="ml-2 underline" onClick={() => setParams({}, { replace: true })}>
            <Trans>Dismiss</Trans>
          </button>
        </p>
      )}
      {linkError && (
        <p role="alert" className="max-w-md rounded bg-red-50 p-3 text-sm text-red-700">
          {i18n._(linkErrorMessages[linkError] ?? linkErrorMessages.link_failed)}
          <button type="button" className="ml-2 underline" onClick={() => setParams({}, { replace: true })}>
            <Trans>Dismiss</Trans>
          </button>
        </p>
      )}

      <section aria-label={t(i18n)`Profile`} className="flex flex-col gap-3">
        <h3 className="text-lg font-semibold"><Trans>Profile</Trans></h3>
        {me.isPending && <p><Trans>Loading...</Trans></p>}
        {me.isError && (
          <p role="alert" className="text-sm text-red-700">
            <Trans>Your profile could not be loaded.</Trans>
          </p>
        )}
        {me.data && <ProfileForm key={me.data.id} me={me.data} />}
        {me.data && <p className="text-sm text-gray-500"><Trans>Email: {email}</Trans></p>}
      </section>

      <section aria-label={t(i18n)`Linked logins`} className="flex flex-col gap-3">
        <h3 className="text-lg font-semibold"><Trans>Linked logins</Trans></h3>
        <p className="max-w-md text-sm text-gray-500">
          <Trans>Any login listed here signs into this account, even when its email differs.</Trans>
        </p>
        {identities.isPending && <p><Trans>Loading...</Trans></p>}
        {identities.isError && (
          <p role="alert" className="text-sm text-red-700">
            <Trans>Linked logins could not be loaded.</Trans>
          </p>
        )}
        {identities.data && <LinkedLogins identities={identities.data} />}
        <div className="flex max-w-md flex-col gap-2">
          {providers.data
            ?.filter((p) => p !== 'dev')
            .map((p) => {
              const providerName = providerNames[p] ?? p
              return (
                <a
                  key={p}
                  href={`/api/auth/link?provider=${p}`}
                  className="rounded border border-gray-300 px-4 py-2 text-center text-sm font-medium hover:bg-gray-50"
                >
                  <Trans>Link {providerName}</Trans>
                </a>
              )
            })}
          {providers.data?.includes('dev') && (
            <div className="mt-1 border-t border-gray-200 pt-3">
              <SectionLabel as="p" size="xs" bold={false} className="mb-2">
                <Trans>Link a dev fixture</Trans>
              </SectionLabel>
              <div className="flex gap-2">
                {devFixtures.map((user) => (
                  <a
                    key={user}
                    href={`/api/auth/link?provider=dev&user=${user}`}
                    className={`${btnPrimary} flex-1`}
                  >
                    {user}
                  </a>
                ))}
              </div>
            </div>
          )}
        </div>
      </section>

      <section aria-label={t(i18n)`Danger zone`} className="flex flex-col gap-3">
        <h3 className="text-lg font-semibold text-red-700"><Trans>Danger zone</Trans></h3>
        <p className="max-w-md text-sm text-gray-500">
          <Trans>
            Deleting your account removes your collection, tags, shelves, linked logins, and
            profile. This cannot be undone.
          </Trans>
        </p>
        <div className="flex items-center gap-3">
          <button
            type="button"
            onClick={() =>
              confirmThen(
                t(i18n)`Delete your account? Your collection, tags, shelves, linked logins, and profile will be permanently removed.`,
                () => removeAccount.mutate(),
              )
            }
            disabled={removeAccount.isPending}
            className="w-fit rounded border border-red-300 px-4 py-2 text-sm font-medium text-red-700 hover:bg-red-50 disabled:opacity-50"
          >
            <Trans>Delete account</Trans>
          </button>
          {removeAccount.isError && (
            <span role="alert" className="text-sm text-red-700">
              <Trans>Deletion did not finish. Nothing is lost ahead of it; try again.</Trans>
            </span>
          )}
        </div>
      </section>
    </main>
  )
}
