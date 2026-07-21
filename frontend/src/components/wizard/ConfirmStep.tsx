import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { useState } from 'react'
import { useNavigate } from 'react-router'
import { ApiError } from '../../api/client'
import { fetchProduct, resolveProduct } from '../../api/catalog'
import { createEntry } from '../../api/collection'
import type { ManualMatch } from '../../lib/catalog'
import { resolveRequestFor } from '../../lib/catalog'
import { useDisplayMoney } from '../../lib/useDisplayMoney'
import type { CatalogPick } from '../catalog/SearchPicker'
import type { DetailsValues } from './DetailsStep'
import { detailsToCreate } from './DetailsStep'
import ConfirmShell from './ConfirmShell'
import ManualMatchPicker from './ManualMatchPicker'

interface ConfirmStepProps {
  pick: CatalogPick
  details: DetailsValues
  // The user's exact listing choice, when one was made; rides the
  // resolve and lands on that listing's own product. onManualMatch
  // reports a choice made HERE (either card's picker) so the wizard
  // state owns it either way.
  manualMatch?: ManualMatch
  onManualMatch: (m: ManualMatch) => void
  onBack: () => void
}

// ConfirmStep resolves the canonical product (find-or-create is
// idempotent by contract, so a query fits despite the POST) and shows
// its price-match status before the entry is created: the user sees
// what "market value" will mean for this copy.
export default function ConfirmStep({ pick, details, manualMatch, onManualMatch, onBack }: ConfirmStepProps) {
  const navigate = useNavigate()
  const queryClient = useQueryClient()
  const money = useDisplayMoney()
  const [matchOpen, setMatchOpen] = useState(false)
  // Community picks name a product that is already minted: fetch it
  // directly and skip the resolve. (The req/queryFn branches both
  // recheck pick.kind, rather than branching on communityId, so each
  // stays narrowed to the provider-only picks resolveRequestFor takes.)
  const communityId = pick.kind === 'community' ? pick.productId : null
  const req = pick.kind === 'community' ? null : resolveRequestFor(pick, manualMatch, details.edition)
  const product = useQuery({
    queryKey: ['resolve', communityId ?? JSON.stringify(req)],
    queryFn: () => (communityId ? fetchProduct(communityId) : resolveProduct(req!)),
    retry: false,
    staleTime: Infinity,
  })
  const create = useMutation({
    mutationFn: () => {
      if (!product.data) throw new Error('no product')
      return createEntry({ product_id: product.data.id, ...detailsToCreate(details, money.profileCurrency) })
    },
    onSuccess: (entry) => {
      void queryClient.invalidateQueries({ queryKey: ['entries'] })
      void queryClient.invalidateQueries({ queryKey: ['platform-facets'] })
      void queryClient.invalidateQueries({ queryKey: ['dashboard'] })
      void queryClient.invalidateQueries({ queryKey: ['recommendations'] })
      void navigate(`/entries/${entry.id}`, { state: { justAdded: true } })
    },
  })

  if (product.isPending) return <p className="py-4">Looking it up...</p>
  if (product.isError) {
    const notFound = product.error instanceof ApiError && product.error.status === 404
    return (
      <div className="py-4">
        <p role="alert" className="rounded bg-red-50 p-3 text-sm text-red-700">
          {manualMatch
            ? 'That listing cannot be matched right now. Go back to change or clear the manual match.'
            : notFound
              ? 'This item is no longer available; try searching again.'
              : 'The lookup failed; your details are kept - try again in a moment.'}
        </p>
        <button onClick={onBack} className="mt-3 rounded border border-gray-300 px-3 py-1 text-sm hover:bg-gray-50">
          Back
        </button>
      </div>
    )
  }

  const p = product.data
  const pc = p.pricecharting
  return (
    <ConfirmShell
      ariaLabel="Confirm"
      title={p.name}
      subtitle={[p.platform?.name, p.type].filter(Boolean).join(' - ')}
      errorMessage={create.isError ? create.error.message || 'The entry could not be created.' : undefined}
      onBack={onBack}
      onSubmit={() => create.mutate()}
      submitPending={create.isPending}
    >
      {matchOpen ? (
        // The picker takes the status card's place while open; the
        // Back and Add actions stay put below it.
        <ManualMatchPicker
          initialQuery={pick.name}
          onPick={(m) => {
            onManualMatch(m)
            setMatchOpen(false)
          }}
          onClose={() => setMatchOpen(false)}
        />
      ) : pc ? (
        <div className="rounded bg-green-50 p-3 text-sm text-green-800">
          <p>
            Priced as "{pc.pc_name}" ({pc.console_name}) - match {Math.round(pc.match_confidence * 100)}%
            {pc.verified ? ', verified' : ''}.
          </p>
          {pick.kind === 'game' && (
            <button
              type="button"
              onClick={() => setMatchOpen(true)}
              className="mt-2 rounded border border-green-300 px-2 py-1 text-sm hover:border-green-400 hover:bg-white"
            >
              Change listing
            </button>
          )}
        </div>
      ) : (
        <div className="rounded bg-gray-50 p-3 text-sm text-gray-600">
          <p>No confirmed price listing yet - market value stays empty until a match is made.</p>
          {pick.kind === 'game' && (
            <button
              type="button"
              onClick={() => setMatchOpen(true)}
              className="mt-2 rounded border border-gray-300 px-2 py-1 text-sm hover:border-gray-400 hover:bg-white"
            >
              Match manually
            </button>
          )}
        </div>
      )}
    </ConfirmShell>
  )
}
