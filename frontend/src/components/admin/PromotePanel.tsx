import { Trans, useLingui } from '@lingui/react/macro'
import { t } from '@lingui/core/macro'
import type { I18n } from '@lingui/core'
import { useMutation, useQueryClient } from '@tanstack/react-query'
import { useState } from 'react'
import { dismissPromoteCandidate, promoteProduct } from '../../api/admin'
import type { PromoteCandidatesPage } from '../../api/admin'
import type { Product } from '../../api/catalog'
import { ApiError } from '../../api/client'
import type { ManualMatch } from '../../lib/catalog'
import SearchPicker from '../catalog/SearchPicker'
import type { CatalogPick } from '../catalog/SearchPicker'
import ManualMatchPicker from '../wizard/ManualMatchPicker'

type Candidate = PromoteCandidatesPage['products'][number]['candidates'][number]

interface PromotePanelProps {
  product: Product
  candidates: Candidate[]
  onDone: () => void
}

// t(i18n) throughout this file, component included: promoteErrorMessage
// is a plain function (cannot call useLingui() itself), so it takes the
// caller's i18n explicitly; the component uses the same explicit form
// for its own strings rather than importing a second, same-named t.
function promoteErrorMessage(e: unknown, i18n: I18n): string {
  if (e instanceof ApiError) {
    // identity_taken names the provider holder; surface it verbatim -
    // this is the true-merge signal an admin acts on by hand.
    if (e.code === 'identity_taken')
      return e.message || t(i18n)`A provider product already holds that identity - nothing changed.`
    if (e.code === 'product_not_community') return t(i18n)`This product is already provider-identified.`
    if (e.code === 'unknown_game' || e.code === 'unknown_pc_product') return t(i18n)`The provider does not know that id.`
    if (e.code === 'upstream_unavailable') return t(i18n)`The provider is unavailable - try again.`
    if (e.message) return e.message
  }
  return t(i18n)`The promotion failed.`
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

  const confirmAnd = (run: () => void) => {
    if (
      window.confirm(
        t(i18n)`Promote this community product to provider identity? Adopter entries upgrade in place; this cannot be undone from the UI.`,
      )
    )
      run()
  }
  const pickGame = (p: CatalogPick) => {
    if (p.kind !== 'game') return
    setPicking(false)
    const identity = { igdb_game_id: p.igdbGameId, platform_igdb_id: p.platformId }
    // The attached listing is optional: without one the body is the two
    // provider ids, exactly as before; a pick adds pc_product_id so the
    // promoted product re-enters the index already priced.
    confirmAnd(() =>
      promote.mutate(listing ? { ...identity, pc_product_id: listing.pcProductId } : identity),
    )
  }

  const seed = candidates[0]?.name ?? product.name
  return (
    <div aria-label={t(i18n)`Promote ${product.name}`} className="mt-2 rounded border border-gray-200 p-3 text-sm">
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
                className="rounded border border-gray-300 px-2 py-0.5 text-xs hover:bg-gray-50 disabled:opacity-50"
              >
                <Trans>Dismiss</Trans>
              </button>
            </li>
          ))}
        </ul>
      )}
      <button
        type="button"
        onClick={() => setPicking(true)}
        className="rounded border border-gray-300 px-3 py-1 hover:bg-gray-50"
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
                <span><Trans>Listing: {listing.name}</Trans></span>
                <button
                  type="button"
                  onClick={() => setListing(null)}
                  className="rounded border border-gray-300 px-2 py-0.5 text-xs hover:bg-gray-50"
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
                className="mt-2 rounded border border-gray-300 px-3 py-1 text-sm hover:bg-gray-50"
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
              confirmAnd(() => promote.mutate({ pc_product_id: m.pcProductId }))
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
