import { useMutation } from '@tanstack/react-query'
import type { Product, ResolveRequest } from '../../api/catalog'
import { resolveProduct } from '../../api/catalog'
import type { CatalogPick } from '../catalog/SearchPicker'
import SearchPicker from '../catalog/SearchPicker'

interface ProxyPickerProps {
  onPick: (product: Product) => void
  onClose: () => void
}

// ProxyPicker chooses a catalog product as an entry's price source:
// the shared search surface plus a resolve to mint/fetch the product.
// The caller owns the PUT that activates it.
export default function ProxyPicker({ onPick, onClose }: ProxyPickerProps) {
  const resolve = useMutation({
    mutationFn: (pick: CatalogPick) => {
      const req: ResolveRequest =
        pick.kind === 'game'
          ? { type: 'game', igdb_game_id: pick.igdbGameId, platform_igdb_id: pick.platformId }
          : { type: pick.category === 'Systems' ? 'console' : 'accessory', pc_product_id: pick.pcProductId }
      return resolveProduct(req)
    },
    onSuccess: (product) => onPick(product),
  })

  return (
    <div role="dialog" aria-label="Choose a price source" className="mt-3 rounded border border-gray-300 bg-gray-50 p-3">
      <div className="mb-2 flex items-center justify-between">
        <p className="text-sm font-semibold">Choose a price source</p>
        <button onClick={onClose} className="text-sm text-gray-500 hover:text-gray-900">
          Close
        </button>
      </div>
      <SearchPicker onPick={(pick) => resolve.mutate(pick)} />
      {resolve.isPending && <p className="mt-2 text-sm text-gray-500">Resolving...</p>}
      {resolve.isError && (
        <p role="alert" className="mt-2 rounded bg-red-50 p-2 text-sm text-red-700">
          That listing cannot be used right now; pick another or try again.
        </p>
      )}
    </div>
  )
}
