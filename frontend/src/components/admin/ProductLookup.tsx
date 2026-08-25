import { Trans, useLingui } from '@lingui/react/macro'
import { useQuery, useQueryClient } from '@tanstack/react-query'
import { useState } from 'react'
import { fetchPromoteCandidates } from '../../api/admin'
import { fetchProduct } from '../../api/catalog'
import { ApiError } from '../../api/client'
import { productTypeWireLabels } from '../../lib/enumLabels'
import { btnSecondary } from '../../lib/formStyles'
import { regionLabelText } from '../../lib/regionLabels'
import MappingFix from './MappingFix'
import PromotePanel from './PromotePanel'

// Remap reach: paste a product id (found while viewing an entry) to bring it
// up regardless of matching state.
export default function ProductLookup() {
  const { t, i18n } = useLingui()
  const queryClient = useQueryClient()
  const [input, setInput] = useState('')
  const [id, setId] = useState('')
  const product = useQuery({
    queryKey: ['admin', 'product', id],
    queryFn: () => fetchProduct(id),
    enabled: id !== '',
    retry: false,
  })
  const candidates = useQuery({
    queryKey: ['admin', 'candidates', id],
    queryFn: () => fetchPromoteCandidates(0, id),
    enabled: id !== '' && product.isSuccess && product.data.origin === 'community',
  })

  const done = () => {
    void queryClient.invalidateQueries({ queryKey: ['admin'] })
  }

  // Hoisted, not called inline in Trans: lingui's message-expression lint
  // wants a plain variable, not a function call.
  const communityRegion = product.isSuccess && product.data.origin === 'community' ? product.data.community?.region : undefined
  const regionLabel = communityRegion ? regionLabelText(i18n, communityRegion) : undefined

  return (
    <section aria-label={t`Product lookup`} className="mt-6">
      <h3 className="text-base font-semibold"><Trans>Product lookup</Trans></h3>
      <form
        className="mt-2 flex gap-2"
        onSubmit={(e) => {
          e.preventDefault()
          const next = input.trim()
          // Resubmitting the shown id is a refresh; same-value setId would be a no-op.
          if (next === id) {
            void product.refetch()
          } else {
            setId(next)
          }
        }}
      >
        <input
          type="text"
          aria-label={t`Product id`}
          value={input}
          onChange={(e) => setInput(e.target.value)}
          placeholder={t`Product id (uuid)`}
          className="w-96 rounded border border-gray-300 px-2 py-1 text-sm"
        />
        <button
          type="submit"
          disabled={input.trim() === ''}
          className={btnSecondary}
        >
          <Trans>Look up</Trans>
        </button>
      </form>
      {product.isFetching && <p className="mt-2 text-sm text-gray-500"><Trans>Looking up...</Trans></p>}
      {product.isError && (
        <p role="alert" className="mt-2 text-sm text-red-700">
          {product.error instanceof ApiError && product.error.status === 404
            ? t`No product with that id.`
            : t`The lookup failed.`}
        </p>
      )}
      {product.isSuccess && (
        <div className="mt-2">
          <p className="text-sm font-semibold">{product.data.name}</p>
          <p className="text-sm text-gray-500">
            {i18n._(productTypeWireLabels[product.data.type])}
            {product.data.platform ? ` - ${product.data.platform.name}` : ''}
          </p>
          {product.data.origin === 'community' && (
            <span className="ml-2 rounded bg-indigo-100 px-1.5 py-0.5 text-xs font-semibold text-indigo-800">
              <Trans>community</Trans>
            </span>
          )}
          {regionLabel && (
            <p className="text-sm text-gray-500">
              <Trans>Region: {regionLabel}</Trans>
            </p>
          )}
          {product.data.origin === 'community' ? (
            // Key on product id: without it, switching to another cached
            // community product reconciles in place, leaking picking state.
            <PromotePanel
              key={product.data.id}
              product={product.data}
              candidates={candidates.data?.products[0]?.candidates ?? []}
              onDone={done}
            />
          ) : (
            <MappingFix product={product.data} onDone={done} />
          )}
        </div>
      )}
    </section>
  )
}
