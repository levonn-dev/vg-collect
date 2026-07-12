import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { useNavigate } from 'react-router'
import { ApiError } from '../../api/client'
import { resolveProduct } from '../../api/catalog'
import { createEntry } from '../../api/collection'
import { resolveRequestFor } from '../../lib/catalog'
import { useDisplayMoney } from '../../lib/useDisplayMoney'
import type { CatalogPick } from '../catalog/SearchPicker'
import type { DetailsValues } from './DetailsStep'
import { detailsToCreate } from './DetailsStep'
import ConfirmShell from './ConfirmShell'

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
    <ConfirmShell
      ariaLabel="Confirm"
      title={p.name}
      subtitle={[p.platform?.name, p.type].filter(Boolean).join(' - ')}
      errorMessage={create.isError ? create.error.message || 'The entry could not be created.' : undefined}
      onBack={onBack}
      onSubmit={() => create.mutate()}
      submitPending={create.isPending}
    >
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
    </ConfirmShell>
  )
}
