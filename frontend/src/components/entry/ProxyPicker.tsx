import { useMutation } from '@tanstack/react-query'
import { useEffect, useRef } from 'react'
import type { Product } from '../../api/catalog'
import { resolveProduct } from '../../api/catalog'
import { resolveRequestFor } from '../../lib/catalog'
import type { CatalogPick } from '../catalog/SearchPicker'
import SearchPicker from '../catalog/SearchPicker'

interface ProxyPickerProps {
  onPick: (product: Product) => void
  onClose: () => void
  // Seeds the search box (and auto-fires the search) - the entry page
  // passes the entry's own title/edition so "change price source"
  // starts from a relevant search rather than empty.
  initialQuery?: string
}

// ProxyPicker chooses a catalog product as an entry's price source:
// the shared search surface plus a resolve to mint/fetch the product.
// The caller owns the PUT that activates it.
export default function ProxyPicker({ onPick, onClose, initialQuery }: ProxyPickerProps) {
  const dialogRef = useRef<HTMLDivElement>(null)

  // A dialog opens on top of the page's own focus; move focus in so
  // keyboard and screen-reader users land inside it, and give it back
  // to whatever had it when this closes (unmounts).
  useEffect(() => {
    const opener = document.activeElement as HTMLElement | null
    dialogRef.current?.querySelector<HTMLElement>('input')?.focus()
    return () => opener?.focus()
  }, [])

  const resolve = useMutation({
    mutationFn: (pick: CatalogPick) => resolveProduct(resolveRequestFor(pick)),
    onSuccess: (product) => onPick(product),
  })

  return (
    <div
      ref={dialogRef}
      role="dialog"
      aria-modal="true"
      aria-label="Choose a price source"
      className="mt-3 rounded border border-gray-300 bg-gray-50 p-3"
    >
      <div className="mb-2 flex items-center justify-between">
        <p className="text-sm font-semibold">Choose a price source</p>
        <button onClick={onClose} className="text-sm text-gray-500 hover:text-gray-900">
          Close
        </button>
      </div>
      <SearchPicker
        kinds={['game', 'hardware', 'pc_listing']}
        initialQuery={initialQuery}
        onPick={(pick) => resolve.mutate(pick)}
      />
      {resolve.isPending && <p className="mt-2 text-sm text-gray-500">Resolving...</p>}
      {resolve.isError && (
        <p role="alert" className="mt-2 rounded bg-red-50 p-2 text-sm text-red-700">
          That listing cannot be used right now; pick another or try again.
        </p>
      )}
    </div>
  )
}
