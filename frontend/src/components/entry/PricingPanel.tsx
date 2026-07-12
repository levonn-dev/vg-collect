import { useQuery } from '@tanstack/react-query'
import { useState } from 'react'
import type { Product } from '../../api/catalog'
import { fetchProduct } from '../../api/catalog'
import type { Entry } from '../../api/collection'
import { dollarsToCents, formatCents } from '../../lib/format'
import { useDisplayMoney } from '../../lib/useDisplayMoney'
import ProxyPicker from './ProxyPicker'

function MatchCard({ product }: { product: Product }) {
  const money = useDisplayMoney()
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
        Loose {money.format(pc.loose_cents) ?? '-'} / CIB {money.format(pc.cib_cents) ?? '-'} / New{' '}
        {money.format(pc.new_cents) ?? '-'}
      </p>
    </div>
  )
}

// The pricing slice of the entry form's draft. customValue is the
// dollars TEXT (converted to cents only at save) so partial input
// like "59." survives typing.
export interface PricingValue {
  mode: Entry['pricing_mode']
  productId?: string
  customValue: string
}

interface PricingPanelProps {
  entry: Entry
  value: PricingValue
  onChange: (v: PricingValue) => void
  // Frozen per the owning form's mount (see EntryForm); the panel never
  // computes it, only labels with it.
  inputCurrency: string
}

// PricingPanel owns every pricing affordance on the entry page, as a
// controlled editor of the form's pricing draft: nothing here talks to
// the server, changes land only with the form's save button. productId
// persists across mode changes by design: off proxy it is "last proxy
// target" memory, and any activation INTO proxy is re-validated by the
// server on save (a vanished target answers 404).
export default function PricingPanel({ entry, value, onChange, inputCurrency }: PricingPanelProps) {
  const [picking, setPicking] = useState(false)
  const money = useDisplayMoney()

  const ownProduct = useQuery({
    queryKey: ['product', entry.product_id],
    queryFn: () => fetchProduct(entry.product_id!),
    enabled: value.mode === 'auto' && !!entry.product_id,
  })
  const targetProduct = useQuery({
    queryKey: ['product', value.productId],
    queryFn: () => fetchProduct(value.productId!),
    enabled: !!value.productId,
  })

  const setMode = (mode: Entry['pricing_mode']) => {
    if (mode === 'proxy' && !value.productId) {
      setPicking(true) // a first proxy still needs a target chosen
    }
    onChange({ ...value, mode })
  }

  const modes: Entry['pricing_mode'][] = entry.product_id
    ? ['auto', 'proxy', 'custom', 'disabled']
    : ['proxy', 'custom', 'disabled']
  const modeHelp: Record<Entry['pricing_mode'], string> = {
    auto: 'auto (this item\'s own price listing)',
    proxy: 'proxy (another listing prices this copy)',
    custom: 'custom (a price you set yourself)',
    disabled: 'disabled (no market value)',
  }

  return (
    <section aria-label="Pricing" className="mb-6 rounded border border-gray-200 p-4">
      <h3 className="text-sm font-semibold uppercase tracking-wide text-gray-500">Pricing</h3>
      <p className="mt-1 text-lg">
        {money.entryValue(entry) ?? 'No market value available.'}
      </p>
      {!money.ready ? (
        <p className="mt-1 text-xs text-gray-500">
          Exchange rates are unavailable; values show in USD.
        </p>
      ) : money.currency === 'USD' ? (
        <p className="mt-1 text-xs text-gray-500">Market values are in USD.</p>
      ) : (
        <p className="mt-1 text-xs text-gray-500">
          Converted from USD at ECB rates ({money.rateDate}
          {money.rateStale ? '; more than a week old' : ''}).
        </p>
      )}

      <fieldset className="mt-3 flex flex-col gap-1" aria-label="Pricing mode">
        {modes.map((m) => (
          <label key={m} className="flex items-center gap-2 text-sm">
            <input
              type="radio"
              name="pricing-mode"
              checked={value.mode === m}
              onChange={() => setMode(m)}
            />
            {modeHelp[m]}
          </label>
        ))}
      </fieldset>

      {value.mode === 'auto' && entry.product_id && (
        <div className="mt-3">
          {ownProduct.isSuccess ? (
            <MatchCard product={ownProduct.data} />
          ) : ownProduct.isError ? (
            <p className="text-sm text-gray-500">The price listing cannot be loaded right now.</p>
          ) : (
            <p className="text-sm text-gray-500">Checking the price match...</p>
          )}
        </div>
      )}

      {value.mode === 'proxy' && value.productId && (
        <div className="mt-3 flex flex-col gap-2">
          <p className="text-sm">
            Price source: <span className="font-medium">{targetProduct.data?.name ?? value.productId}</span>
          </p>
          {targetProduct.isSuccess && <MatchCard product={targetProduct.data} />}
          {!entry.product_id && entry.item_type === 'game' && (
            <p className="text-xs text-gray-500">
              Recommendations treat this copy as the proxied game.
            </p>
          )}
          <button
            type="button"
            onClick={() => setPicking(true)}
            className="self-start rounded border border-gray-300 px-2 py-1 text-sm hover:bg-gray-50"
          >
            Change price source
          </button>
        </div>
      )}

      {value.mode === 'proxy' && !value.productId && (
        <div className="mt-3 flex flex-col gap-2">
          <p className="text-sm text-gray-600">No price source chosen yet.</p>
          <button
            type="button"
            onClick={() => setPicking(true)}
            className="self-start rounded border border-gray-300 px-2 py-1 text-sm hover:bg-gray-50"
          >
            Choose price source
          </button>
        </div>
      )}

      {value.mode === 'custom' && (
        <div className="mt-3 flex flex-col gap-2">
          <label className="flex flex-col gap-1 text-sm font-medium">
            Custom price ({inputCurrency})
            <input
              inputMode="decimal"
              value={value.customValue}
              onChange={(e) => onChange({ ...value, customValue: e.target.value })}
              placeholder="59.99"
              className="w-32 rounded border border-gray-300 px-2 py-1 text-sm"
            />
          </label>
          {entry.custom_value_set_at && (
            <p className="text-xs text-gray-500">
              Price set on {new Date(entry.custom_value_set_at).toLocaleDateString()}.
            </p>
          )}
        </div>
      )}

      {value.mode !== 'proxy' && value.productId && (
        <p className="mt-3 flex items-center gap-2 rounded bg-gray-50 p-2 text-sm text-gray-600">
          Last price proxy: {targetProduct.data?.name ?? value.productId}
          <button
            type="button"
            onClick={() => onChange({ ...value, mode: 'proxy' })}
            aria-label="Reactivate the last price proxy"
            className="rounded border border-gray-300 px-2 py-0.5 text-xs hover:bg-gray-100"
          >
            Reactivate
          </button>
        </p>
      )}

      {value.mode !== 'custom' && dollarsToCents(value.customValue) !== undefined && (
        <p className="mt-3 flex items-center gap-2 rounded bg-gray-50 p-2 text-sm text-gray-600">
          Last custom price: {formatCents(dollarsToCents(value.customValue), inputCurrency)}
          <button
            type="button"
            onClick={() => onChange({ ...value, mode: 'custom' })}
            aria-label="Reactivate the last custom price"
            className="rounded border border-gray-300 px-2 py-0.5 text-xs hover:bg-gray-100"
          >
            Reactivate
          </button>
        </p>
      )}

      {picking && (
        <ProxyPicker
          initialQuery={[entry.display_name, entry.edition].filter(Boolean).join(' ')}
          onClose={() => setPicking(false)}
          onPick={(product) => {
            onChange({ mode: 'proxy', productId: product.id, customValue: value.customValue })
            setPicking(false)
          }}
        />
      )}
    </section>
  )
}
