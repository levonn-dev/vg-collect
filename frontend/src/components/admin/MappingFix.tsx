import { Trans, useLingui } from '@lingui/react/macro'
import { msg, t } from '@lingui/core/macro'
import type { I18n, MessageDescriptor } from '@lingui/core'
import { useMutation } from '@tanstack/react-query'
import { useState } from 'react'
import { deleteProduct, setProductMapping } from '../../api/admin'
import type { Product } from '../../api/catalog'
import { ApiError } from '../../api/client'
import { confirmThen } from '../../lib/confirm'
import { resolveApiError } from '../../lib/resolveApiError'
import ManualMatchPicker from '../wizard/ManualMatchPicker'

interface MappingFixProps {
  product: Product
  onDone: () => void
}

const fixErrorCodes: Record<string, MessageDescriptor> = {
  identity_taken: msg`Another product already carries that identity - the mapping was not changed.`,
  unknown_pc_product: msg`PriceCharting does not know that listing.`,
  upstream_unavailable: msg`The price provider is unavailable - try again.`,
}

// This file's own strings use the explicit t(i18n) form (not the
// useLingui()-bound t) so they match resolveApiError's own
// explicit-i18n signature without importing a second, same-named t.
//
// identity_taken's server detail names the holding product; surface it
// verbatim (ahead of resolveApiError's own code lookup) so the admin
// can look the holder up, same as any other code's server detail would
// win once no code matches at all - this is that same preference,
// just also applying to a code that DOES match.
function fixErrorMessage(e: unknown, i18n: I18n): string {
  if (e instanceof ApiError && e.code === 'identity_taken' && e.message) return e.message
  return resolveApiError(e, i18n, fixErrorCodes, msg`The mapping change failed.`)
}

// MappingFix is the admin correction surface for one product: set a
// mapping through the same listing picker the add wizard uses, clear
// it (a clear sets match_hold, so it asks first), or - for unmatched
// residue whose listings all belong to siblings - park it with Hold
// (the same PUT null; the entry rematch stops retrying, any set lifts it).
export default function MappingFix({ product, onDone }: MappingFixProps) {
  const { i18n } = useLingui()
  const [pickerOpen, setPickerOpen] = useState(false)
  const fix = useMutation({
    mutationFn: (pcProductId: number | null) => setProductMapping(product.id, pcProductId),
    onSuccess: onDone,
  })

  const clear = () =>
    confirmThen(
      t(i18n)`Clear this mapping? The product becomes unmatched and is held out of the nightly entry rematch.`,
      () => fix.mutate(null),
    )

  const hold = () =>
    confirmThen(
      t(i18n)`Hold this product out of the nightly entry rematch? Setting any mapping lifts the hold.`,
      () => fix.mutate(null),
    )

  const del = useMutation({
    mutationFn: () => deleteProduct(product.id),
    onSuccess: onDone,
  })

  const remove = () =>
    confirmThen(
      t(i18n)`Delete this product from the catalog? Only unmatched products that no entries reference can be deleted.`,
      () => del.mutate(),
    )

  const pc = product.pricecharting
  const productName = product.name
  const pcName = pc?.pc_name
  const consoleName = pc?.console_name
  const confidence = pc ? Math.round(pc.match_confidence * 100) : undefined
  return (
    <div className="mt-2 rounded border border-gray-200 p-3" aria-label={t(i18n)`Fix mapping for ${productName}`}>
      <p className="text-sm">
        {pc ? (
          pc.verified ? (
            t(i18n)`Mapped to "${pcName}" (${consoleName}), match ${confidence}%, verified`
          ) : (
            t(i18n)`Mapped to "${pcName}" (${consoleName}), match ${confidence}%`
          )
        ) : (
          <Trans>Unmatched</Trans>
        )}
        {product.match_hold && (
          <span className="ml-2 rounded bg-amber-50 px-1.5 py-0.5 text-xs font-semibold text-amber-800">
            <Trans>held</Trans>
          </span>
        )}
      </p>
      <div className="mt-2 flex gap-2">
        <button
          type="button"
          onClick={() => setPickerOpen(true)}
          disabled={fix.isPending}
          className="rounded border border-gray-300 px-3 py-1 text-sm hover:bg-gray-50 disabled:opacity-50"
        >
          <Trans>Choose listing</Trans>
        </button>
        {pc && (
          <button
            type="button"
            onClick={clear}
            disabled={fix.isPending}
            className="rounded border border-gray-300 px-3 py-1 text-sm hover:bg-gray-50 disabled:opacity-50"
          >
            <Trans>Clear mapping</Trans>
          </button>
        )}
        {!pc && !product.match_hold && (
          <button
            type="button"
            onClick={hold}
            disabled={fix.isPending}
            className="rounded border border-gray-300 px-3 py-1 text-sm hover:bg-gray-50 disabled:opacity-50"
          >
            <Trans>Hold</Trans>
          </button>
        )}
        {!pc && (
          <button
            type="button"
            onClick={remove}
            disabled={fix.isPending || del.isPending}
            className="rounded border border-gray-300 px-3 py-1 text-sm hover:bg-gray-50 disabled:opacity-50"
          >
            <Trans>Delete</Trans>
          </button>
        )}
      </div>
      {(fix.isError || del.isError) && (
        <p role="alert" className="mt-2 text-sm text-red-700">
          {fixErrorMessage(fix.isError ? fix.error : del.error, i18n)}
        </p>
      )}
      {pickerOpen && (
        <ManualMatchPicker
          initialQuery={product.name}
          onPick={(m) => {
            setPickerOpen(false)
            fix.mutate(m.pcProductId)
          }}
          onClose={() => setPickerOpen(false)}
        />
      )}
    </div>
  )
}
