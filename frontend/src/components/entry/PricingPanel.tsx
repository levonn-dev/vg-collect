import { Trans, useLingui } from '@lingui/react/macro'
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { useState } from 'react'
import type { Product } from '../../api/catalog'
import { fetchProduct, resolveProduct } from '../../api/catalog'
import type { Entry } from '../../api/collection'
import { updateEntry } from '../../api/collection'
import type { ManualMatch } from '../../lib/catalog'
import { invalidateEntryQueries } from '../../lib/entryQueries'
import { entryToUpdate } from '../../lib/entryUpdate'
import { dollarsToCents, formatCents, formatDate } from '../../lib/format'
import { btnSecondary } from '../../lib/formStyles'
import { refetchWarning, renderQueryState } from '../../lib/queryBoundary'
import { useDisplayMoney } from '../../lib/useDisplayMoney'
import MatchStatusCard from '../MatchStatusCard'
import SectionLabel from '../SectionLabel'
import ManualMatchPicker from '../catalog/ManualMatchPicker'
import ProxyPicker from './ProxyPicker'

// MatchCard owns the no-match paragraph itself (a bare <p>, unlike
// ConfirmStep's boxed gray branch below it) and otherwise defers to
// the shared status card, with prices on - the one difference from
// ConfirmStep's own (prices-off) use of that same card.
function MatchCard({ product }: { product: Product }) {
  const pc = product.pricecharting
  if (!pc) {
    return (
      <p className="rounded bg-gray-50 p-3 text-sm text-gray-600">
        <Trans>No confirmed price listing yet - market value stays empty until a match is made.</Trans>
      </p>
    )
  }
  return <MatchStatusCard pc={pc} showPrices />
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
  const { t } = useLingui()
  const [picking, setPicking] = useState(false)
  const [matching, setMatching] = useState(false)
  const queryClient = useQueryClient()
  // Narrow re-match: an auto-priced entry on an unmatched game product
  // may move onto the listing the user picks. The resolve lands on
  // that listing's product (same game and platform - identity is
  // listing-keyed) and the entry repoints to it. Unlike every other
  // control here, this saves immediately: it is a catalog identity
  // action, not a draft field, and the server validates the narrow
  // conditions again. The button below gates on entry.pricing_mode
  // (the SAVED mode), not value.mode (the draft), because this
  // immediate PUT resends the stored entry, not the draft.
  const rematch = useMutation({
    mutationFn: async (m: ManualMatch) => {
      const p = ownProduct.data
      if (!p?.igdb || !p.platform) throw new Error('product identity unavailable')
      const member = await resolveProduct({
        type: 'game',
        igdb_game_id: p.igdb.game_id,
        platform_igdb_id: p.platform.igdb_platform_id,
        pc_product_id: m.pcProductId,
      })
      return updateEntry(entry.id, { ...entryToUpdate(entry), product_id: member.id })
    },
    onSuccess: () => {
      void queryClient.invalidateQueries({ queryKey: ['entry', entry.id] })
      invalidateEntryQueries(queryClient, [['product', entry.product_id]])
    },
  })
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
    auto: t`auto (this item's own price listing)`,
    proxy: t`proxy (another listing prices this copy)`,
    custom: t`custom (a price you set yourself)`,
    disabled: t`disabled (no market value)`,
  }
  const rateDate = money.rateDate
  const staleNote = money.rateStale ? t`; more than a week old` : ''
  const proxyName = targetProduct.data?.name ?? value.productId
  const priceSetDate = entry.custom_value_set_at ? formatDate(entry.custom_value_set_at) : ''
  const lastCustomPrice = formatCents(dollarsToCents(value.customValue), inputCurrency)

  return (
    <section aria-label={t`Pricing`} className="mb-6 rounded border border-gray-200 p-4">
      <SectionLabel as="h3" size="sm"><Trans>Pricing</Trans></SectionLabel>
      <p className="mt-1 text-lg">
        {money.entryValue(entry) ?? t`No market value available.`}
      </p>
      {!money.ready ? (
        <p className="mt-1 text-xs text-gray-500">
          <Trans>Exchange rates are unavailable; values show in USD.</Trans>
        </p>
      ) : money.currency === 'USD' ? (
        <p className="mt-1 text-xs text-gray-500"><Trans>Market values are in USD.</Trans></p>
      ) : (
        <p className="mt-1 text-xs text-gray-500">
          <Trans>
            Converted from USD at ECB rates ({rateDate}
            {staleNote}).
          </Trans>
        </p>
      )}

      <fieldset className="mt-3 flex flex-col gap-1" aria-label={t`Pricing mode`}>
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
          {ownProduct.data !== undefined ? (
            <>
              {refetchWarning(ownProduct)}
              <MatchCard product={ownProduct.data} />
              {entry.pricing_mode === 'auto' && ownProduct.data.type === 'game' && !ownProduct.data.pricecharting &&
                ownProduct.data.igdb && ownProduct.data.platform && (
                  <button
                    type="button"
                    onClick={() => setMatching(true)}
                    disabled={rematch.isPending}
                    className={`${btnSecondary} mt-2`}
                  >
                    <Trans>Match listing</Trans>
                  </button>
                )}
              {rematch.isError && (
                <p role="alert" className="mt-2 rounded bg-red-50 p-2 text-sm text-red-700">
                  <Trans>The listing match failed; try again in a moment.</Trans>
                </p>
              )}
            </>
          ) : (
            // No role: a still-checking or momentarily-unavailable match
            // status is not worth interrupting a screen reader over, unlike
            // every role="alert" boundary elsewhere in this file.
            renderQueryState(ownProduct, {
              size: 'subsection',
              loading: <Trans>Checking the price match...</Trans>,
              error: <Trans>The price listing cannot be loaded right now.</Trans>,
            })
          )}
        </div>
      )}

      {value.mode === 'proxy' && value.productId && (
        <div className="mt-3 flex flex-col gap-2">
          <p className="text-sm">
            <Trans>
              Price source: <span className="font-medium">{proxyName}</span>
            </Trans>
          </p>
          {targetProduct.isSuccess && <MatchCard product={targetProduct.data} />}
          {!entry.product_id && entry.item_type === 'game' && (
            <p className="text-xs text-gray-500">
              <Trans>Recommendations treat this copy as the proxied game.</Trans>
            </p>
          )}
          <button
            type="button"
            onClick={() => setPicking(true)}
            className={`${btnSecondary} self-start`}
          >
            <Trans>Change price source</Trans>
          </button>
        </div>
      )}

      {value.mode === 'proxy' && !value.productId && (
        <div className="mt-3 flex flex-col gap-2">
          <p className="text-sm text-gray-600"><Trans>No price source chosen yet.</Trans></p>
          <button
            type="button"
            onClick={() => setPicking(true)}
            className={`${btnSecondary} self-start`}
          >
            <Trans>Choose price source</Trans>
          </button>
        </div>
      )}

      {value.mode === 'custom' && (
        <div className="mt-3 flex flex-col gap-2">
          <label className="flex flex-col gap-1 text-sm font-medium">
            <Trans>Custom price ({inputCurrency})</Trans>
            <input
              inputMode="decimal"
              value={value.customValue}
              onChange={(e) => onChange({ ...value, customValue: e.target.value })}
              placeholder={t`59.99`}
              className="w-32 rounded border border-gray-300 px-2 py-1 text-sm"
            />
          </label>
          {entry.custom_value_set_at && (
            <p className="text-xs text-gray-500">
              <Trans>Price set on {priceSetDate}.</Trans>
            </p>
          )}
        </div>
      )}

      {value.mode !== 'proxy' && value.productId && (
        <p className="mt-3 flex items-center gap-2 rounded bg-gray-50 p-2 text-sm text-gray-600">
          <Trans>Last price proxy: {proxyName}</Trans>
          <button
            type="button"
            onClick={() => onChange({ ...value, mode: 'proxy' })}
            aria-label={t`Reactivate the last price proxy`}
            className="rounded border border-gray-300 px-2 py-0.5 text-xs hover:bg-gray-100"
          >
            <Trans>Reactivate</Trans>
          </button>
        </p>
      )}

      {value.mode !== 'custom' && dollarsToCents(value.customValue) !== undefined && (
        <p className="mt-3 flex items-center gap-2 rounded bg-gray-50 p-2 text-sm text-gray-600">
          <Trans>Last custom price: {lastCustomPrice}</Trans>
          <button
            type="button"
            onClick={() => onChange({ ...value, mode: 'custom' })}
            aria-label={t`Reactivate the last custom price`}
            className="rounded border border-gray-300 px-2 py-0.5 text-xs hover:bg-gray-100"
          >
            <Trans>Reactivate</Trans>
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

      {matching && (
        <ManualMatchPicker
          initialQuery={entry.display_name}
          onPick={(m) => {
            setMatching(false)
            rematch.mutate(m)
          }}
          onClose={() => setMatching(false)}
        />
      )}
    </section>
  )
}
