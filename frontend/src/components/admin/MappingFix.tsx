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

function fixErrorMessage(e: unknown): string {
  if (e instanceof ApiError) {
    // The server's identity_taken detail names the holding product;
    // surface it verbatim so the admin can look the holder up.
    if (e.code === 'identity_taken')
      return e.message || 'Another product already carries that identity - the mapping was not changed.'
    if (e.code === 'unknown_pc_product') return 'PriceCharting does not know that listing.'
    if (e.code === 'upstream_unavailable') return 'The price provider is unavailable - try again.'
    if (e.message) return e.message
  }
  return 'The mapping change failed.'
}

// MappingFix is the admin correction surface for one product: set a
// mapping through the same listing picker the add wizard uses, clear
// it (a clear sets match_hold, so it asks first), or - for unmatched
// residue whose listings all belong to siblings - park it with Hold
// (the same PUT null; the walk stops retrying, any set lifts it).
export default function MappingFix({ product, onDone }: MappingFixProps) {
  const [pickerOpen, setPickerOpen] = useState(false)
  const fix = useMutation({
    mutationFn: (pcProductId: number | null) => setProductMapping(product.id, pcProductId),
    onSuccess: onDone,
  })

  const clear = () => {
    if (
      !window.confirm(
        'Clear this mapping? The product becomes unmatched and is held out of the nightly re-match walk.',
      )
    )
      return
    fix.mutate(null)
  }

  const hold = () => {
    if (
      !window.confirm(
        'Hold this product out of the nightly re-match walk? Setting any mapping lifts the hold.',
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
        'Delete this product from the catalog? Only unmatched products that no entries reference can be deleted.',
      )
    )
      return
    del.mutate()
  }

  const pc = product.pricecharting
  return (
    <div className="mt-2 rounded border border-gray-200 p-3" aria-label={`Fix mapping for ${product.name}`}>
      <p className="text-sm">
        {pc ? (
          <>
            Mapped to &quot;{pc.pc_name}&quot; ({pc.console_name}), match{' '}
            {Math.round(pc.match_confidence * 100)}%{pc.verified && ', verified'}
          </>
        ) : (
          <>Unmatched</>
        )}
        {product.match_hold && (
          <span className="ml-2 rounded bg-amber-100 px-1.5 py-0.5 text-xs font-semibold text-amber-800">
            held
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
          Choose listing
        </button>
        {pc && (
          <button
            type="button"
            onClick={clear}
            disabled={fix.isPending}
            className="rounded border border-gray-300 px-3 py-1 text-sm hover:bg-gray-50 disabled:opacity-50"
          >
            Clear mapping
          </button>
        )}
        {!pc && !product.match_hold && (
          <button
            type="button"
            onClick={hold}
            disabled={fix.isPending}
            className="rounded border border-gray-300 px-3 py-1 text-sm hover:bg-gray-50 disabled:opacity-50"
          >
            Hold
          </button>
        )}
        {!pc && (
          <button
            type="button"
            onClick={remove}
            disabled={fix.isPending || del.isPending}
            className="rounded border border-gray-300 px-3 py-1 text-sm hover:bg-gray-50 disabled:opacity-50"
          >
            Delete
          </button>
        )}
      </div>
      {(fix.isError || del.isError) && (
        <p role="alert" className="mt-2 text-sm text-red-700">
          {fixErrorMessage(fix.isError ? fix.error : del.error)}
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
