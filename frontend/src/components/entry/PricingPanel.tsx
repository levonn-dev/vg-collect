import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { useState } from 'react'
import type { Product } from '../../api/catalog'
import { fetchProduct } from '../../api/catalog'
import type { Entry, EntryUpdate } from '../../api/collection'
import { updateEntry } from '../../api/collection'
import { entryToUpdate } from '../../lib/entryUpdate'
import { formatCents } from '../../lib/format'
import ProxyPicker from './ProxyPicker'

function MatchCard({ product }: { product: Product }) {
  const pc = product.pricecharting
  if (!pc) {
    return (
      <p className="rounded bg-gray-50 p-3 text-sm text-gray-600">
        No confirmed price listing yet - market value stays empty until a match is made.
      </p>
    )
  }
  return (
    <div className="rounded bg-green-50 p-3 text-sm text-green-900">
      <p>
        Priced as "{pc.pc_name}" ({pc.console_name}) - match {Math.round(pc.match_confidence * 100)}%
        {pc.verified ? ', verified' : ''}.
      </p>
      <p className="mt-1 text-xs text-green-800">
        Loose {formatCents(pc.loose_cents) ?? '-'} / CIB {formatCents(pc.cib_cents) ?? '-'} / New{' '}
        {formatCents(pc.new_cents) ?? '-'}
      </p>
    </div>
  )
}

// PricingPanel owns every pricing affordance on the entry page. Mode
// changes ride the full-PUT baseline, so nothing else on the entry
// moves. pricing_product_id persists across mode changes by design:
// off proxy it is "last proxy target" memory, and any activation INTO
// proxy is re-validated by the server (a vanished target answers 404).
export default function PricingPanel({ entry }: { entry: Entry }) {
  const queryClient = useQueryClient()
  const [picking, setPicking] = useState(false)

  const ownProduct = useQuery({
    queryKey: ['product', entry.product_id],
    queryFn: () => fetchProduct(entry.product_id!),
    enabled: entry.pricing_mode === 'auto' && !!entry.product_id,
  })
  const targetProduct = useQuery({
    queryKey: ['product', entry.pricing_product_id],
    queryFn: () => fetchProduct(entry.pricing_product_id!),
    enabled: !!entry.pricing_product_id,
  })

  const save = useMutation({
    mutationFn: (update: EntryUpdate) => updateEntry(entry.id, update),
    onSuccess: (updated) => {
      queryClient.setQueryData(['entry', entry.id], updated)
      void queryClient.invalidateQueries({ queryKey: ['entries'] })
      void queryClient.invalidateQueries({ queryKey: ['dashboard'] })
      void queryClient.invalidateQueries({ queryKey: ['recommendations'] })
      setPicking(false)
    },
  })

  const setMode = (mode: Entry['pricing_mode']) => {
    if (mode === 'proxy' && !entry.pricing_product_id) {
      setPicking(true) // a first proxy needs a target before the PUT
      return
    }
    save.mutate({ ...entryToUpdate(entry), pricing_mode: mode })
  }

  const modes: Entry['pricing_mode'][] = entry.product_id
    ? ['auto', 'proxy', 'disabled']
    : ['proxy', 'disabled']
  const modeHelp: Record<Entry['pricing_mode'], string> = {
    auto: 'auto (its own catalog listing)',
    proxy: 'proxy (another listing prices this copy)',
    disabled: 'disabled (no market value)',
  }

  return (
    <section aria-label="Pricing" className="mb-6 rounded border border-gray-200 p-4">
      <h3 className="text-sm font-semibold uppercase tracking-wide text-gray-500">Pricing</h3>
      <p className="mt-1 text-lg">
        {formatCents(entry.value_cents) ?? 'No market value available.'}
      </p>

      <fieldset className="mt-3 flex flex-col gap-1" aria-label="Pricing mode">
        {modes.map((m) => (
          <label key={m} className="flex items-center gap-2 text-sm">
            <input
              type="radio"
              name="pricing-mode"
              checked={entry.pricing_mode === m}
              onChange={() => setMode(m)}
              disabled={save.isPending}
            />
            {modeHelp[m]}
          </label>
        ))}
      </fieldset>

      {entry.pricing_mode === 'auto' && entry.product_id && (
        <div className="mt-3">
          {ownProduct.isSuccess ? (
            <MatchCard product={ownProduct.data} />
          ) : ownProduct.isError ? (
            <p className="text-sm text-gray-500">The catalog listing cannot be loaded right now.</p>
          ) : (
            <p className="text-sm text-gray-500">Checking the price match...</p>
          )}
        </div>
      )}

      {entry.pricing_mode === 'proxy' && entry.pricing_product_id && (
        <div className="mt-3 flex flex-col gap-2">
          <p className="text-sm">
            Price source: <span className="font-medium">{targetProduct.data?.name ?? entry.pricing_product_id}</span>
          </p>
          {targetProduct.isSuccess && <MatchCard product={targetProduct.data} />}
          {!entry.product_id && entry.item_type === 'game' && (
            <p className="text-xs text-gray-500">
              Recommendations treat this copy as the proxied game.
            </p>
          )}
          <button
            onClick={() => setPicking(true)}
            className="self-start rounded border border-gray-300 px-2 py-1 text-sm"
          >
            Change price source
          </button>
        </div>
      )}

      {entry.pricing_mode !== 'proxy' && entry.pricing_product_id && (
        <p className="mt-3 flex items-center gap-2 rounded bg-gray-50 p-2 text-sm text-gray-600">
          Last price proxy: {targetProduct.data?.name ?? entry.pricing_product_id}
          <button
            onClick={() => save.mutate({ ...entryToUpdate(entry), pricing_mode: 'proxy' })}
            disabled={save.isPending}
            className="rounded border border-gray-300 px-2 py-0.5 text-xs disabled:opacity-50"
          >
            Reactivate
          </button>
        </p>
      )}

      {save.isError && (
        <p role="alert" className="mt-3 rounded bg-red-50 p-3 text-sm text-red-700">
          {save.error.message || 'The pricing change could not be saved.'}
        </p>
      )}

      {picking && (
        <ProxyPicker
          onClose={() => setPicking(false)}
          onPick={(product) =>
            save.mutate({
              ...entryToUpdate(entry),
              pricing_mode: 'proxy',
              pricing_product_id: product.id,
            })
          }
        />
      )}
    </section>
  )
}
