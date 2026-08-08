import { Trans, useLingui } from '@lingui/react/macro'
import { t } from '@lingui/core/macro'
import type { I18n } from '@lingui/core'
import { useMutation } from '@tanstack/react-query'
import { useState } from 'react'
import { deleteProduct, setProductMapping } from '../../api/admin'
import type { Product } from '../../api/catalog'
import { ApiError } from '../../api/client'
import ManualMatchPicker from '../wizard/ManualMatchPicker'

interface MappingFixProps {
  product: Product
  onDone: () => void
}

// t(i18n) throughout this file, component included: fixErrorMessage is
// a plain function (cannot call useLingui() itself), so it takes the
// caller's i18n explicitly; the component uses the same explicit form
// for its own strings rather than importing a second, same-named t.
function fixErrorMessage(e: unknown, i18n: I18n): string {
  if (e instanceof ApiError) {
    // The server's identity_taken detail names the holding product;
    // surface it verbatim so the admin can look the holder up.
    if (e.code === 'identity_taken')
      return e.message || t(i18n)`Another product already carries that identity - the mapping was not changed.`
    if (e.code === 'unknown_pc_product') return t(i18n)`PriceCharting does not know that listing.`
    if (e.code === 'upstream_unavailable') return t(i18n)`The price provider is unavailable - try again.`
    if (e.message) return e.message
  }
  return t(i18n)`The mapping change failed.`
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

  const clear = () => {
    if (
      !window.confirm(
        t(i18n)`Clear this mapping? The product becomes unmatched and is held out of the nightly entry rematch.`,
      )
    )
      return
    fix.mutate(null)
  }

  const hold = () => {
    if (
      !window.confirm(
        t(i18n)`Hold this product out of the nightly entry rematch? Setting any mapping lifts the hold.`,
      )
    )
      return
    fix.mutate(null)
  }

  const del = useMutation({
    mutationFn: () => deleteProduct(product.id),
    onSuccess: onDone,
  })

  const remove = () => {
    if (
      !window.confirm(
        t(i18n)`Delete this product from the catalog? Only unmatched products that no entries reference can be deleted.`,
      )
    )
      return
    del.mutate()
  }

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
