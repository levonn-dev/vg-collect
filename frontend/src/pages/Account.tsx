import { Trans, useLingui } from '@lingui/react/macro'
import { msg, t } from '@lingui/core/macro'
import type { I18n, MessageDescriptor } from '@lingui/core'
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { useState } from 'react'
import { useNavigate, useSearchParams } from 'react-router'
import {
  ApiError,
  deleteAccount,
  fetchIdentities,
  fetchMe,
  fetchProviders,
  unlinkIdentity,
  updateMe,
  type Identity,
  type Me,
} from '../api/client'
import CopyButton from '../components/CopyButton'

// Proper nouns only (leave-alone list) - never wrapped for translation.
// An unknown provider id falls back to itself, same as before.
const providerNames: Record<string, string> = {
  google: 'Google',
  twitch: 'Twitch',
}

const devFixtures = ['alice', 'bob', 'admin']

const linkErrorMessages: Record<string, MessageDescriptor> = {
  conflict: msg`That login already belongs to another account, so it was not linked.`,
  email_unverified: msg`That login has no verified email address; verify it there and try again.`,
  link_failed: msg`Linking failed. Please try again.`,
}

const visibilityOptions: [Me['profile_visibility'], MessageDescriptor][] = [
  ['private', msg`Private - only you`],
  ['unlisted', msg`Unlisted - anyone signed in who has your link`],
  ['listed', msg`Listed - appears in Explore and search`],
]

const landingPageOptions: [Me['landing_page'], MessageDescriptor][] = [
  ['feed', msg`Feed`],
  ['collection', msg`Collection`],
  ['explore', msg`Explore`],
]

// t(i18n) throughout this file, every component included:
// saveErrorMessage is a plain function (cannot call useLingui() itself),
// so it takes the caller's i18n explicitly; the components below use
// the same explicit form for their own strings rather than importing a
// second, same-named t.
//
// The handle-cooldown and taken-handle problems get their own copy;
// anything else (validation, network) falls back to the generic line.
function saveErrorMessage(error: unknown, i18n: I18n): string {
  if (error instanceof ApiError) {
    if (error.code === 'handle_taken') return t(i18n)`That handle is taken.`
    if (error.code === 'handle_cooldown') return t(i18n)`Handle changed too recently - try again later.`
    if (error.message) return error.message
  }
  return t(i18n)`Saving failed. Please try again.`
}

// ProfileForm is keyed by me.id at the call site so its local draft
// state seeds once per loaded profile.
function ProfileForm({ me }: { me: Me }) {
  const { i18n } = useLingui()
  const [handle, setHandle] = useState(me.handle)
  const [avatarUrl, setAvatarUrl] = useState(me.avatar_url ?? '')
  const [visibility, setVisibility] = useState(me.profile_visibility)
  const [landingPage, setLandingPage] = useState(me.landing_page)
  const queryClient = useQueryClient()
  const save = useMutation({
    mutationFn: () =>
      updateMe({
        handle,
        avatar_url: avatarUrl,
        profile_visibility: visibility,
        landing_page: landingPage,
      }),
    onSuccess: () => void queryClient.invalidateQueries({ queryKey: ['me'] }),
  })

  return (
    <form
      className="flex max-w-md flex-col gap-3"
      onSubmit={(e) => {
        e.preventDefault()
        save.mutate()
      }}
    >
      <div className="flex flex-col gap-1">
        <label htmlFor="handle" className="text-sm text-gray-700">
          <Trans>Handle</Trans>
        </label>
        <input
          id="handle"
          value={handle}
          onChange={(e) => setHandle(e.target.value)}
          required
          minLength={2}
          maxLength={30}
          pattern="[a-zA-Z0-9](?:[a-zA-Z0-9_]{0,28}[a-zA-Z0-9])?"
          title={t(i18n)`2-30 characters, letters/digits, underscores inside only`}
          className="rounded border border-gray-300 px-3 py-2"
        />
      </div>
      <div className="flex flex-col gap-1">
        <label htmlFor="avatar-url" className="text-sm text-gray-700">
          <Trans>Avatar image URL</Trans>
        </label>
        <input
          id="avatar-url"
          type="url"
          value={avatarUrl}
          onChange={(e) => setAvatarUrl(e.target.value)}
          placeholder="https://..."
          className="rounded border border-gray-300 px-3 py-2"
        />
        <p className="text-xs text-gray-500"><Trans>Leave empty to use your initial instead.</Trans></p>
      </div>
      <fieldset className="flex flex-col gap-1">
        <legend className="text-sm text-gray-700"><Trans>Profile visibility</Trans></legend>
        {visibilityOptions.map(([value, label]) => (
          <label key={value} className="flex items-center gap-2 text-sm">
            <input
              type="radio"
              name="profile_visibility"
              value={value}
              checked={visibility === value}
              onChange={() => setVisibility(value)}
            />
            {i18n._(label)}
          </label>
        ))}
      </fieldset>
      <fieldset className="flex flex-col gap-1">
        <legend className="text-sm text-gray-700"><Trans>Default page</Trans></legend>
        <p className="text-xs text-gray-500"><Trans>Where the app opens after you sign in.</Trans></p>
        {landingPageOptions.map(([value, label]) => (
          <label key={value} className="flex items-center gap-2 text-sm">
            <input
              type="radio"
              name="landing_page"
              value={value}
              checked={landingPage === value}
              onChange={() => setLandingPage(value)}
            />
            {i18n._(label)}
          </label>
        ))}
      </fieldset>
      {me.profile_visibility !== 'private' && (
        <CopyButton
          text={`${location.origin}/u/${me.handle}`}
          label={t(i18n)`Copy profile link`}
          className="self-start px-3 py-1 text-sm"
        />
      )}
      <div className="flex items-center gap-3">
        <button
          type="submit"
          disabled={save.isPending}
          className="rounded bg-gray-900 px-4 py-2 text-sm font-medium text-white hover:bg-gray-700 disabled:opacity-50"
        >
          <Trans>Save</Trans>
        </button>
        {save.isSuccess && (
          <span role="status" className="text-sm text-green-800">
            <Trans>Saved.</Trans>
          </span>
        )}
        {save.isError && (
          <span role="alert" className="text-sm text-red-700">
            {saveErrorMessage(save.error, i18n)}
          </span>
        )}
      </div>
    </form>
  )
}

function LinkedLogins({ identities }: { identities: Identity[] }) {
  const { i18n } = useLingui()
  const queryClient = useQueryClient()
  const unlink = useMutation({
    mutationFn: unlinkIdentity,
    onSuccess: () => void queryClient.invalidateQueries({ queryKey: ['identities'] }),
  })
  const lastOne = identities.length === 1

  return (
    <ul className="flex max-w-md flex-col gap-2">
      {identities.map((identity) => (
        <li
          key={identity.id}
          className="flex items-center justify-between rounded border border-gray-200 px-3 py-2"
        >
          <div>
            <p className="text-sm font-medium capitalize">{identity.provider}</p>
            <p className="text-xs text-gray-500">
              <Trans>
                {identity.email ?? t(i18n)`no email recorded`} - linked{' '}
                {new Date(identity.created_at).toLocaleDateString()}
              </Trans>
            </p>
          </div>
          <button
            onClick={() => {
              if (window.confirm(t(i18n)`Unlink this login? You will no longer be able to sign in with it.`))
                unlink.mutate(identity.id)
            }}
            disabled={lastOne || unlink.isPending}
            title={lastOne ? t(i18n)`Your account needs at least one login` : undefined}
            className="rounded border border-gray-300 px-3 py-1 text-sm hover:bg-gray-50 disabled:opacity-50"
          >
            <Trans>Unlink</Trans>
          </button>
        </li>
      ))}
      {unlink.isError && (
        <li role="alert" className="text-sm text-red-700">
          <Trans>Unlinking failed. Please try again.</Trans>
        </li>
      )}
    </ul>
  )
}

// Account is the self-service page: profile fields, linked provider
// logins (link, unlink), and account deletion. Link buttons are full
// navigations like login: the OAuth dance needs real redirects.
export default function Account() {
  const { i18n } = useLingui()
  const [params, setParams] = useSearchParams()
  const navigate = useNavigate()
  const queryClient = useQueryClient()
  const me = useQuery({ queryKey: ['me'], queryFn: fetchMe })
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

  return (
    <main className="flex flex-col gap-8 py-6">
      <h2 className="text-2xl font-bold"><Trans>Account</Trans></h2>

      {linked && (
        <p role="status" className="max-w-md rounded bg-green-50 p-3 text-sm text-green-800">
          <Trans>Login linked. Signing in with it now lands in this account.</Trans>
          <button className="ml-2 underline" onClick={() => setParams({}, { replace: true })}>
            <Trans>Dismiss</Trans>
          </button>
        </p>
      )}
      {linkError && (
        <p role="alert" className="max-w-md rounded bg-red-50 p-3 text-sm text-red-700">
          {i18n._(linkErrorMessages[linkError] ?? linkErrorMessages.link_failed)}
          <button className="ml-2 underline" onClick={() => setParams({}, { replace: true })}>
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
        {me.data && <p className="text-sm text-gray-500"><Trans>Email: {me.data.email}</Trans></p>}
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
            .map((p) => (
              <a
                key={p}
                href={`/api/auth/link?provider=${p}`}
                className="rounded border border-gray-300 px-4 py-2 text-center text-sm font-medium hover:bg-gray-50"
              >
                <Trans>Link {providerNames[p] ?? p}</Trans>
              </a>
            ))}
          {providers.data?.includes('dev') && (
            <div className="mt-1 border-t border-gray-200 pt-3">
              <p className="mb-2 text-xs uppercase tracking-wide text-gray-500">
                <Trans>Link a dev fixture</Trans>
              </p>
              <div className="flex gap-2">
                {devFixtures.map((user) => (
                  <a
                    key={user}
                    href={`/api/auth/link?provider=dev&user=${user}`}
                    className="flex-1 rounded bg-gray-900 px-3 py-2 text-center text-sm text-white hover:bg-gray-700"
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
            onClick={() => {
              if (
                window.confirm(
                  t(i18n)`Delete your account? Your collection, tags, shelves, linked logins, and profile will be permanently removed.`,
                )
              )
                removeAccount.mutate()
            }}
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
