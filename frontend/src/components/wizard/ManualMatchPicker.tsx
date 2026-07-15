import { useEffect, useRef } from 'react'
import type { ManualMatch } from '../../lib/catalog'
import type { CatalogPick } from '../catalog/SearchPicker'
import SearchPicker from '../catalog/SearchPicker'

interface ManualMatchPickerProps {
  // Seeds the listing search (the game's name) so matching starts from
  // relevant candidates; the console column disambiguates.
  initialQuery: string
  onPick: (m: ManualMatch) => void
  onClose: () => void
}

// ManualMatchPicker chooses the exact PriceCharting listing for a game
// being added. Search only - no resolve here; the choice rides the
// game resolve so the product mints (or fills) with that mapping.
// Same dialog conventions as ProxyPicker.
export default function ManualMatchPicker({ initialQuery, onPick, onClose }: ManualMatchPickerProps) {
  const dialogRef = useRef<HTMLDivElement>(null)

  // A dialog opens on top of the page's own focus; move focus in so
  // keyboard and screen-reader users land inside it, and give it back
  // to whatever had it when this closes (unmounts).
  useEffect(() => {
    const opener = document.activeElement as HTMLElement | null
    dialogRef.current?.querySelector<HTMLElement>('input')?.focus()
    return () => opener?.focus()
  }, [])

  const pickListing = (pick: CatalogPick) => {
    if (pick.kind === 'pc_listing') onPick({ pcProductId: pick.pcProductId, name: pick.name })
  }

  return (
    <div
      ref={dialogRef}
      role="dialog"
      aria-modal="true"
      aria-label="Match a price listing"
      className="mt-3 rounded border border-gray-300 bg-gray-50 p-3"
    >
      <div className="mb-2 flex items-center justify-between">
        <p className="text-sm font-semibold">Match a price listing</p>
        <button onClick={onClose} className="text-sm text-gray-500 hover:text-gray-900">
          Close
        </button>
      </div>
      <SearchPicker kinds={['pc_listing']} initialQuery={initialQuery} onPick={pickListing} />
    </div>
  )
}
