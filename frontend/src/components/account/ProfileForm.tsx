import { Trans, useLingui } from '@lingui/react/macro'
import { msg, t } from '@lingui/core/macro'
import type { I18n, MessageDescriptor } from '@lingui/core'
import { useMutation, useQueryClient } from '@tanstack/react-query'
import { useState } from 'react'
import { updateMe, type Me } from '../../api/me'
import CopyButton from '../CopyButton'
import { Handle } from '../../gen/facets'
import { landingPageValues, visibilityValues } from '../../api/schema'
import { btnPrimary } from '../../lib/formStyles'
import { resolveApiError } from '../../lib/resolveApiError'

const HANDLE_MIN = Handle.minLength
const HANDLE_MAX = Handle.maxLength
const HANDLE_PATTERN = Handle.pattern
const VISIBILITY_VALUES = visibilityValues
type Visibility = (typeof VISIBILITY_VALUES)[number]
type LandingPage = (typeof landingPageValues)[number]

// The HTML pattern attribute is implicitly anchored at both ends, so
// the generated HANDLE_PATTERN's own ^...$ would be redundant on the
// wire - stripped once here rather than baked into the constant.
const handlePatternAttr = HANDLE_PATTERN.replace(/^\^|\$$/g, '')

const visibilityLabelText: Record<Visibility, MessageDescriptor> = {
  private: msg`Private - only you`,
  unlisted: msg`Unlisted - anyone signed in who has your link`,
  listed: msg`Listed - appears in Explore and search`,
}
// VISIBILITY_VALUES already orders private/unlisted/listed - the radio
// group's existing display order - so mapping over it is a pure
// source-of-truth swap, not a reorder.
const visibilityOptions: [Visibility, MessageDescriptor][] = VISIBILITY_VALUES.map((v): [Visibility, MessageDescriptor] => [v, visibilityLabelText[v]])

const landingPageLabelText: Record<LandingPage, MessageDescriptor> = {
  feed: msg`Feed`,
  collection: msg`Collection`,
  explore: msg`Explore`,
}
// Display order (feed first, the common case) is a UX choice
// independent of the wire enum's own declared order - typed against
// the generated LandingPage so a removed value fails to compile here
// instead of lingering as a dead option.
const LANDING_PAGE_DISPLAY_ORDER: readonly LandingPage[] = ['feed', 'collection', 'explore']
const landingPageOptions: [LandingPage, MessageDescriptor][] = LANDING_PAGE_DISPLAY_ORDER.map((v): [LandingPage, MessageDescriptor] => [v, landingPageLabelText[v]])

const saveErrorCodes: Record<string, MessageDescriptor> = {
  handle_taken: msg`That handle is taken.`,
  handle_cooldown: msg`Handle changed too recently - try again later.`,
}

// t(i18n) throughout this file: its strings use the explicit form
// (not the useLingui()-bound t) so they match resolveApiError's own
// explicit-i18n signature without importing a second, same-named t.
function saveErrorMessage(error: unknown, i18n: I18n): string {
  return resolveApiError(error, i18n, saveErrorCodes, msg`Saving failed. Please try again.`)
}

// ProfileForm is keyed by me.id at the call site so its local draft
// state seeds once per loaded profile.
export default function ProfileForm({ me }: { me: Me }) {
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
          minLength={HANDLE_MIN}
          maxLength={HANDLE_MAX}
          pattern={handlePatternAttr}
          title={t(i18n)`${HANDLE_MIN}-${HANDLE_MAX} characters, letters/digits, underscores inside only`}
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
          className={btnPrimary}
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
