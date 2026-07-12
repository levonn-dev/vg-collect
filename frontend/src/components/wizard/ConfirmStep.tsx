import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { useNavigate } from 'react-router'
import { ApiError } from '../../api/client'
import type { ResolveRequest } from '../../api/catalog'
import { resolveProduct } from '../../api/catalog'
import { createEntry } from '../../api/collection'
import { useDisplayMoney } from '../../lib/useDisplayMoney'
import type { CatalogPick } from '../catalog/SearchPicker'
import type { DetailsValues } from './DetailsStep'
import { detailsToCreate } from './DetailsStep'

function resolveRequestFor(pick: CatalogPick): ResolveRequest {
  if (pick.kind === 'game') {
    return { type: 'game', igdb_game_id: pick.igdbGameId, platform_igdb_id: pick.platformId }
  }
  if (pick.kind === 'pc_listing') {
    return { type: 'pc_listing', pc_product_id: pick.pcProductId }
  }
  // PriceCharting's Systems category maps to console; everything else
  // it lists for hardware (controllers, accessories) is an accessory.
  return { type: pick.category === 'Systems' ? 'console' : 'accessory', pc_product_id: pick.pcProductId }
}

interface ConfirmStepProps {
  pick: CatalogPick
  details: DetailsValues
  onBack: () => void
}

// ConfirmStep resolves the canonical product (find-or-create is
// idempotent by contract, so a query fits despite the POST) and shows
// its price-match status before the entry is created: the user sees
// what "market value" will mean for this copy.
export default function ConfirmStep({ pick, details, onBack }: ConfirmStepProps) {
  const navigate = useNavigate()
  const queryClient = useQueryClient()
  const money = useDisplayMoney()
  const req = resolveRequestFor(pick)
  const product = useQuery({
    queryKey: ['resolve', JSON.stringify(req)],
    queryFn: () => resolveProduct(req),
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
          {notFound
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
    <section aria-label="Confirm" className="flex flex-col gap-3">
      <h3 className="text-lg font-semibold">Confirm: {p.name}</h3>
      <p className="text-sm text-gray-600">
        {[p.platform?.name, p.type].filter(Boolean).join(' - ')}
      </p>
      {pc ? (
        <p className="rounded bg-green-50 p-3 text-sm text-green-800">
          Priced as "{pc.pc_name}" ({pc.console_name}) - match {Math.round(pc.match_confidence * 100)}%
          {pc.verified ? ', verified' : ''}.
        </p>
      ) : (
        <p className="rounded bg-gray-50 p-3 text-sm text-gray-600">
          No confirmed price listing yet - market value stays empty until a match is made.
        </p>
      )}
      {create.isError && (
        <p role="alert" className="rounded bg-red-50 p-3 text-sm text-red-700">
          {create.error.message || 'The entry could not be created.'}
        </p>
      )}
      <div className="flex gap-2">
        <button onClick={onBack} className="rounded border border-gray-300 px-3 py-1 text-sm hover:bg-gray-50">
          Back
        </button>
        <button
          onClick={() => create.mutate()}
          disabled={create.isPending}
          className="rounded bg-gray-900 px-4 py-1 text-sm text-white enabled:hover:bg-gray-700 disabled:opacity-50"
        >
          Add to collection
        </button>
      </div>
    </section>
  )
}
