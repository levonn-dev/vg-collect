import { Trans, useLingui } from '@lingui/react/macro'
import type { ManualMatch } from '../../lib/catalog'
import SearchPickerDialog from '../SearchPickerDialog'
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
// game resolve, which lands on that listing's own product (game
// identity is listing-keyed). Same dialog shell as ProxyPicker.
export default function ManualMatchPicker({ initialQuery, onPick, onClose }: ManualMatchPickerProps) {
  const { t } = useLingui()

  const pickListing = (pick: CatalogPick) => {
    if (pick.kind === 'pc_listing') onPick({ pcProductId: pick.pcProductId, name: pick.name })
  }

  return (
    <SearchPickerDialog
      ariaLabel={t`Match a price listing`}
      title={<Trans>Match a price listing</Trans>}
      onClose={onClose}
    >
      <SearchPicker kinds={['pc_listing']} initialQuery={initialQuery} onPick={pickListing} />
    </SearchPickerDialog>
  )
}
