import { Trans, useLingui } from '@lingui/react/macro'
import { msg } from '@lingui/core/macro'
import type { I18n, MessageDescriptor } from '@lingui/core'
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import type { ReactNode } from 'react'
import { Link } from 'react-router'
import type { SavedView } from '../../api/collection'
import { fetchViews, updateView } from '../../api/collection'
import { visibilityWireLabels } from '../../lib/enumLabels'
import { resolveApiError } from '../../lib/resolveApiError'
import { useMe } from '../../lib/useMe'
import CopyButton from '../CopyButton'
import VisibilityControl from '../social/VisibilityControl'

// Badge classes stay on the gray/amber/green ladder that mirrors
// private/unlisted/listed elsewhere (VisibilityControl's active
// segment, ApprovalNotice's status colors).
const visibilityBadges: Record<SavedView['visibility'], string> = {
  private: 'bg-gray-100 text-gray-700',
  unlisted: 'bg-amber-50 text-amber-800',
  listed: 'bg-green-50 text-green-800',
}

// view_exists (a name conflict) is left out: this mutation resends the
// shelf's own unchanged name (see changeVisibility below), which never
// conflicts with itself, so the endpoint's other documented code never
// answers a visibility-only PUT.
const changeVisibilityErrorCodes: Record<string, MessageDescriptor> = {
  view_not_found: msg`This shelf no longer exists.`,
}
function changeVisibilityErrorMessage(e: unknown, i18n: I18n): string {
  return resolveApiError(e, i18n, changeVisibilityErrorCodes, msg`The shelf visibility update failed.`)
}

// ShelfManager is the Shelves tab's own content: the per-shelf
// management list (ViewPicker itself renders only the quick-row - see
// that file). It runs its own me/views queries - views is a cache hit
// against the key ViewPicker populates from the Items tab, me against
// the key the app shell resolves at login - and owns its own
// changeVisibility mutation and that mutation's own error surface.
//
// The optional notice at the top warns about a mismatch between the
// owner's profile visibility and a shelf's own visibility: a private
// profile hides every shelf regardless of the shelf's own setting, and
// an unlisted profile keeps listed shelves out of Explore even though
// they are still link-reachable. At most one of the two notes ever
// applies, since profile_visibility is a single value.
export default function ShelfManager() {
  const { t, i18n } = useLingui()
  const queryClient = useQueryClient()
  const me = useMe()
  const views = useQuery({ queryKey: ['views'], queryFn: fetchViews })
  const myHandle = me.data?.handle

  // changeVisibility carries the shelf's OWN stored name/params (not
  // any currently-applied list state), so flipping visibility on any
  // row never overwrites that shelf's saved filters.
  const changeVisibility = useMutation({
    mutationFn: (vars: { view: SavedView; visibility: SavedView['visibility'] }) =>
      updateView(vars.view.id, vars.view.name, vars.view.params, vars.visibility),
    onSuccess: () => void queryClient.invalidateQueries({ queryKey: ['views'] }),
  })

  const shelves = views.data ?? []
  const hasNonPrivateShelf = shelves.some((v) => v.visibility !== 'private')
  const hasListedShelf = shelves.some((v) => v.visibility === 'listed')

  let note: ReactNode = null
  if (me.data?.profile_visibility === 'private' && hasNonPrivateShelf) {
    note = (
      <Trans>
        Your profile is private, so other people cannot see any of your shelves
        regardless of their own setting. Change profile visibility{' '}
        <Link to="/account" className="underline">in Account</Link>.
      </Trans>
    )
  } else if (me.data?.profile_visibility === 'unlisted' && hasListedShelf) {
    note = (
      <Trans>
        Your profile is unlisted, so listed shelves are reachable by link only - they will not appear in Explore.
      </Trans>
    )
  }

  return (
    <section aria-label={t`Manage shelves`} className="flex flex-col gap-3">
      {note && (
        <p role="note" className="rounded bg-amber-50 p-3 text-sm text-amber-800">
          {note}
        </p>
      )}
      {changeVisibility.error && (
        <p role="alert" className="text-xs text-red-700">
          {changeVisibilityErrorMessage(changeVisibility.error, i18n)}
        </p>
      )}
      {views.data && (
        shelves.length === 0 ? (
          <p className="text-sm text-gray-500"><Trans>No shelves yet. Save one from the Items tab.</Trans></p>
        ) : (
          <ul className="flex flex-col gap-1">
            {shelves.map((v) => (
              <li key={v.id} className="flex flex-wrap items-center gap-2 text-sm">
                <span className="min-w-0 flex-1 truncate">{v.name}</span>
                <span
                  className={`rounded px-1.5 py-0.5 text-xs font-semibold ${visibilityBadges[v.visibility]}`}
                >
                  {i18n._(visibilityWireLabels[v.visibility])}
                </span>
                <VisibilityControl
                  value={v.visibility}
                  disabled={changeVisibility.isPending}
                  onChange={(next) => changeVisibility.mutate({ view: v, visibility: next })}
                />
                {v.visibility !== 'private' && myHandle && (
                  <CopyButton
                    text={`${location.origin}/u/${myHandle}/shelves/${v.slug}`}
                    className="px-2 py-1 text-xs"
                  />
                )}
              </li>
            ))}
          </ul>
        )
      )}
    </section>
  )
}
