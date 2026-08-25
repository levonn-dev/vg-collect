import { Trans, useLingui } from '@lingui/react/macro'
import type { ManualMatch } from '../../lib/catalog'
import SearchPickerDialog from '../SearchPickerDialog'
import type { CatalogPick } from '../../lib/catalogPicks'
import SearchPicker from './SearchPicker'

interface ManualMatchPickerProps {
  // Seeds the listing search (game name); console column disambiguates.
  initialQuery: string
  onPick: (m: ManualMatch) => void
  onClose: () => void
}

// Search only, no resolve: the choice rides the game resolve, which lands on
// that listing's own product (game identity is listing-keyed).
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
