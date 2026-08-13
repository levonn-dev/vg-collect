import { Trans, useLingui } from '@lingui/react/macro'
import { useMutation } from '@tanstack/react-query'
import type { Product } from '../../api/catalog'
import { fetchProduct, resolveProduct } from '../../api/catalog'
import { resolveRequestFor } from '../../lib/catalog'
import SearchPickerDialog from '../SearchPickerDialog'
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
  const { t } = useLingui()

  const resolve = useMutation({
    // The community lane is suppressed here (communityLane="hidden"):
    // community products are priceless, so they are not price sources.
    // The community branch stays as a defensive guard - SearchPicker's
    // onPick type still admits a CommunityPick - and narrows the pick so
    // resolveRequestFor only ever sees a resolvable kind.
    mutationFn: (pick: CatalogPick) =>
      pick.kind === 'community' ? fetchProduct(pick.productId) : resolveProduct(resolveRequestFor(pick)),
    onSuccess: (product) => onPick(product),
  })

  return (
    <SearchPickerDialog
      ariaLabel={t`Choose a price source`}
      title={<Trans>Choose a price source</Trans>}
      onClose={onClose}
    >
      <SearchPicker
        kinds={['game', 'hardware', 'pc_listing']}
        communityLane="hidden"
        initialQuery={initialQuery}
        onPick={(pick) => resolve.mutate(pick)}
      />
      {resolve.isPending && <p className="mt-2 text-sm text-gray-500"><Trans>Resolving...</Trans></p>}
      {resolve.isError && (
        <p role="alert" className="mt-2 rounded bg-red-50 p-2 text-sm text-red-700">
          <Trans>That listing cannot be used right now; pick another or try again.</Trans>
        </p>
      )}
    </SearchPickerDialog>
  )
}
