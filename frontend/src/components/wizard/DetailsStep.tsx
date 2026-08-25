import { Trans, useLingui } from '@lingui/react/macro'
import { useState } from 'react'
import type { EntryCreate } from '../../api/collection'
import { EntryCreate as EntryCreateFacet } from '../../gen/facets'
import type { ManualMatch } from '../../lib/catalog'
import { dollarsToCents } from '../../lib/format'
import { btnPrimary, btnSecondary, btnSecondaryXs, labelClass } from '../../lib/formStyles'
import type { EntryRegion, LocalizationBundle } from '../../lib/productTitle'
import { regionTitle, titleFormFor } from '../../lib/productTitle'
import type { CopyDetailsValues } from '../entry/CopyDetailsFields'
import { CopyDetailsFields } from '../entry/CopyDetailsFields'
import ManualMatchPicker from '../catalog/ManualMatchPicker'

// Alias keeps the wizard's own vocabulary at its call sites (AddWizard and
// ConfirmStep pass DetailsValues between steps).
export type DetailsValues = CopyDetailsValues

const DEFAULT_STATUS = EntryCreateFacet.properties.status.default
const DEFAULT_MEDIA_TYPE = EntryCreateFacet.properties.media_type.default

// eslint-disable-next-line react-refresh/only-export-components -- shared with ConfirmStep and the test, alongside this component.
export function defaultDetails(region: string = 'ntsc_u'): DetailsValues {
  return {
    region, edition: '', packaging: 'cib', hasBox: true, hasManual: true,
    boxCondition: '', manualCondition: '', itemCondition: '', pricePaid: '',
    purchasedAt: '', purchasedFrom: '', status: DEFAULT_STATUS,
    rating: '', notes: '', storageLocation: '', pinned: false,
  }
}

// pricing_mode/match_provenance are both auto; the custom path overrides
// pricing_mode to disabled, ConfirmStep overrides match_provenance to user,
// both after spreading. currency is the caller's stamp, needing no rate.
// eslint-disable-next-line react-refresh/only-export-components -- shared with ConfirmStep and the test, alongside this component.
export function detailsToCreate(d: DetailsValues, currency: string): Omit<EntryCreate, 'product_id' | 'display_name' | 'item_type' | 'platform_name' | 'first_release_date'> {
  return {
    media_type: DEFAULT_MEDIA_TYPE,
    region: d.region,
    edition: d.edition.trim() === '' ? undefined : d.edition.trim(),
    packaging: d.packaging,
    has_box: d.hasBox,
    has_manual: d.hasManual,
    box_condition: d.hasBox && d.boxCondition !== '' ? d.boxCondition : undefined,
    manual_condition: d.hasManual && d.manualCondition !== '' ? d.manualCondition : undefined,
    item_condition: d.itemCondition === '' ? undefined : d.itemCondition,
    price_paid_cents: dollarsToCents(d.pricePaid),
    currency,
    purchased_at: d.purchasedAt === '' ? undefined : d.purchasedAt,
    purchased_from: d.purchasedFrom.trim() === '' ? undefined : d.purchasedFrom.trim(),
    pricing_mode: 'auto',
    match_provenance: 'auto',
    status: d.status,
    rating: d.rating === '' ? undefined : Number(d.rating),
    notes: d.notes.trim() === '' ? undefined : d.notes,
    storage_location: d.storageLocation.trim() === '' ? undefined : d.storageLocation.trim(),
    pinned: d.pinned,
  }
}

interface DetailsStepProps {
  // Heading follows the selected region through regionTitle, so a JP copy
  // reads by its JP identity while the select sits on ntsc_j.
  product: { name: string; localizations?: LocalizationBundle[] }
  // Platform's own region set (game picks only): renders the Region select
  // grouped, that set first - guidance, not enforcement.
  regionGroup?: { platformName: string; regions: EntryRegion[] }
  // Label only: price-paid is stamped with this at create time
  // (detailsToCreate takes it separately), never edited here.
  currency: string
  // Seeds the form when a caller remounts with values already collected (e.g.
  // wizard Back); omitted, the step starts blank.
  initialValues?: DetailsValues
  // Manual price match (game catalog path only): row renders only when the
  // callback is given; custom/hardware paths pass neither.
  manualMatch?: ManualMatch | null
  onManualMatchChange?: (m: ManualMatch | null) => void
  // Seeds the listing search when the dialog opens.
  manualMatchQuery?: string
  onBack: () => void
  onNext: (d: DetailsValues) => void
}

export default function DetailsStep({ product, regionGroup, currency, initialValues, manualMatch, onManualMatchChange, manualMatchQuery, onBack, onNext }: DetailsStepProps) {
  const { t, i18n } = useLingui()
  const [v, setV] = useState<DetailsValues>(() => initialValues ?? defaultDetails())
  const [matchOpen, setMatchOpen] = useState(false)

  // Heading follows the live-selected region, re-deriving on every render.
  const form = titleFormFor(i18n.locale)
  const title = regionTitle(product.name, product.localizations, v.region, form)
  const titleText = title.text

  return (
    <>
      <form
        onSubmit={(e) => {
          e.preventDefault()
          onNext(v)
        }}
        aria-label={t`Copy details`}
        className="flex flex-col gap-4"
      >
        <h3 className="text-lg font-semibold">
          <Trans>Your copy of <span lang={title.lang}>{titleText}</span></Trans>
        </h3>
        <CopyDetailsFields
          value={v}
          onChange={setV}
          currencyLabel={currency}
          editionLabel={t`Edition or variant`}
          editionPlaceholder={t`first print, players choice...`}
          regionGroup={regionGroup}
        >
          {onManualMatchChange && (
            <div className={labelClass}>
              <Trans>Price listing match (optional)</Trans>
              {manualMatch ? (
                <span className="flex items-center gap-2 font-normal">
                  {manualMatch.name}
                  <button
                    type="button"
                    onClick={() => onManualMatchChange(null)}
                    className={btnSecondaryXs}
                  >
                    <Trans>Clear</Trans>
                  </button>
                </span>
              ) : (
                <span className="flex items-center gap-2">
                  <button
                    type="button"
                    onClick={() => setMatchOpen(true)}
                    className={btnSecondary}
                  >
                    <Trans>Match manually</Trans>
                  </button>
                  <span className="text-xs font-normal text-gray-500"><Trans>Otherwise auto-match picks the listing.</Trans></span>
                </span>
              )}
            </div>
          )}
        </CopyDetailsFields>
        <div className="flex gap-2">
          <button type="button" onClick={onBack} className={btnSecondary}>
            <Trans>Back</Trans>
          </button>
          <button type="submit" className={btnPrimary}>
            <Trans>Continue</Trans>
          </button>
        </div>
      </form>
      {matchOpen && onManualMatchChange && (
        <ManualMatchPicker
          initialQuery={manualMatchQuery ?? ''}
          onPick={(m) => {
            onManualMatchChange(m)
            setMatchOpen(false)
          }}
          onClose={() => setMatchOpen(false)}
        />
      )}
    </>
  )
}
