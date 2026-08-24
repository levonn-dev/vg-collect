import { Trans, useLingui } from '@lingui/react/macro'
import { msg, t } from '@lingui/core/macro'
import type { I18n, MessageDescriptor } from '@lingui/core'
import { useMutation, useQueryClient } from '@tanstack/react-query'
import { useState } from 'react'
import { dismissPromoteCandidate, promoteProduct } from '../../api/admin'
import type { PromoteCandidatesPage } from '../../api/admin'
import type { Product } from '../../api/catalog'
import { ApiError } from '../../api/client'
import type { ManualMatch } from '../../lib/catalog'
import { confirmThen } from '../../lib/confirm'
import { btnSecondary, btnSecondaryXs } from '../../lib/formStyles'
import { resolveApiError } from '../../lib/resolveApiError'
import SearchPicker from '../catalog/SearchPicker'
import type { CatalogPick } from '../../lib/catalogPicks'
import ManualMatchPicker from '../catalog/ManualMatchPicker'

type Candidate = PromoteCandidatesPage['products'][number]['candidates'][number]

interface PromotePanelProps {
  product: Product
  candidates: Candidate[]
  onDone: () => void
}

const promoteErrorCodes: Record<string, MessageDescriptor> = {
  identity_taken: msg`A provider product already holds that identity - nothing changed.`,
  product_not_community: msg`This product is already provider-identified.`,
  unknown_game: msg`The provider does not know that id.`,
  unknown_pc_product: msg`The provider does not know that id.`,
  upstream_unavailable: msg`The provider is unavailable - try again.`,
}

// This file's own strings use the explicit t(i18n) form (not the
// useLingui()-bound t) so they match resolveApiError's own
// explicit-i18n signature without importing a second, same-named t.
//
// identity_taken names the provider holder; surface it verbatim (ahead
// of resolveApiError's own code lookup) - this is the true-merge signal
// an admin acts on by hand, so it wins even though the code itself
// already matches a known entry.
function promoteErrorMessage(e: unknown, i18n: I18n): string {
  if (e instanceof ApiError && e.code === 'identity_taken' && e.message) return e.message
  return resolveApiError(e, i18n, promoteErrorCodes, msg`The promotion failed.`)
}

// PromotePanel is a community product's upgrade surface: confirm the
// provider identity through the live pickers (a candidate pre-seeds
// the search) and promote in place - the product id stays stable, so
// every adopter upgrades through live reads. Dismiss silences a
// wrong candidate permanently.
export default function PromotePanel({ product, candidates, onDone }: PromotePanelProps) {
  const { i18n } = useLingui()
  const queryClient = useQueryClient()
  const [picking, setPicking] = useState(false)
  const [attaching, setAttaching] = useState(false)
  const [listing, setListing] = useState<ManualMatch | null>(null)
  const promote = useMutation({
    mutationFn: (req: Parameters<typeof promoteProduct>[1]) => promoteProduct(product.id, req),
    onSuccess: onDone,
  })
  const dismiss = useMutation({
    mutationFn: (c: Candidate) => dismissPromoteCandidate(product.id, c.provider, c.provider_id),
    // Dismiss silences one wrong candidate, not the whole review: refresh
    // the list (the parent re-feeds this panel's candidates) but keep the
    // panel open so the remaining candidates stay reviewable. Only a
    // promote closes it.
    onSuccess: () => void queryClient.invalidateQueries({ queryKey: ['admin'] }),
  })

  // Both promote paths (game pick and manual match pick below) ask the
  // exact same question, so the message is computed once here rather
  // than repeated at each confirmThen call site.
  const promoteMessage = t(i18n)`Promote this community product to provider identity? Adopter entries upgrade in place; this cannot be undone from the UI.`
  const pickGame = (p: CatalogPick) => {
    if (p.kind !== 'game') return
    setPicking(false)
    const identity = { igdb_game_id: p.igdbGameId, platform_igdb_id: p.platformId }
    // The attached listing is optional: without one the body is the two
    // provider ids, exactly as before; a pick adds pc_product_id so the
    // promoted product re-enters the index already priced.
    confirmThen(promoteMessage, () =>
      promote.mutate(listing ? { ...identity, pc_product_id: listing.pcProductId } : identity),
    )
  }

  const seed = candidates[0]?.name ?? product.name
  const listingName = listing?.name
  const productName = product.name
  return (
    <div aria-label={t(i18n)`Promote ${productName}`} className="mt-2 rounded border border-gray-200 p-3 text-sm">
      {candidates.length > 0 && (
        <ul className="mb-2">
          {candidates.map((c) => (
            <li key={`${c.provider}:${c.provider_id}`} className="flex items-center justify-between py-0.5">
              <span>
                {c.name} <span className="text-gray-500">({c.provider}, {c.score.toFixed(2)})</span>
              </span>
              <button
                type="button"
                onClick={() => dismiss.mutate(c)}
                disabled={dismiss.isPending}
                className={btnSecondaryXs}
              >
                {/* Its own catalog entry, distinct from the transient
                    banner Dismiss (DismissibleNotice/Account): this one
                    silences a wrong candidate permanently, not closes a
                    notice, and a translator needs that difference. */}
                <Trans context="silence a promote candidate">Dismiss</Trans>
              </button>
            </li>
          ))}
        </ul>
      )}
      <button
        type="button"
        onClick={() => setPicking(true)}
        className={btnSecondary}
      >
        <Trans>Promote to provider identity</Trans>
      </button>
      {picking &&
        (product.type === 'game' ? (
          <div className="mt-2">
            <SearchPicker kinds={['game']} initialQuery={seed} onPick={pickGame} />
            {/* Optional: attach a price listing before picking the game
                (the game pick fires the promote). No attach keeps the
                body at the two provider ids. */}
            {listing ? (
              <p className="mt-2 flex items-center gap-2 text-sm">
                <span><Trans>Listing: {listingName}</Trans></span>
                <button
                  type="button"
                  onClick={() => setListing(null)}
                  className={btnSecondaryXs}
                >
                  <Trans>Clear</Trans>
                </button>
              </p>
            ) : attaching ? (
              <ManualMatchPicker
                initialQuery={seed}
                onPick={(m) => {
                  setListing(m)
                  setAttaching(false)
                }}
                onClose={() => setAttaching(false)}
              />
            ) : (
              <button
                type="button"
                onClick={() => setAttaching(true)}
                className={`${btnSecondary} mt-2`}
              >
                <Trans>Attach a price listing (optional)</Trans>
              </button>
            )}
          </div>
        ) : (
          <ManualMatchPicker
            initialQuery={seed}
            onPick={(m) => {
              setPicking(false)
              confirmThen(promoteMessage, () => promote.mutate({ pc_product_id: m.pcProductId }))
            }}
            onClose={() => setPicking(false)}
          />
        ))}
      {promote.isError && (
        <p role="alert" className="mt-2 text-red-700">
          {promoteErrorMessage(promote.error, i18n)}
        </p>
      )}
      {dismiss.isError && (
        <p role="alert" className="mt-2 text-red-700">
          <Trans>The dismissal failed - try again.</Trans>
        </p>
      )}
    </div>
  )
}
