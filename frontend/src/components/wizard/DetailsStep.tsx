import { Trans, useLingui } from '@lingui/react/macro'
import { useState } from 'react'
import type { EntryCreate } from '../../api/collection'
import type { ManualMatch } from '../../lib/catalog'
import { dollarsToCents } from '../../lib/format'
import { inputClass, labelClass } from '../../lib/formStyles'
import { CONDITIONS, PACKAGINGS, STATUSES } from '../../lib/listParams'
import type { EntryRegion, LocalizationBundle } from '../../lib/productTitle'
import { regionTitle, titleFormFor } from '../../lib/productTitle'
import RegionPicker from '../catalog/RegionPicker'
import ManualMatchPicker from './ManualMatchPicker'

type Condition = NonNullable<EntryCreate['item_condition']>

export interface DetailsValues {
  region: string
  edition: string
  packaging: EntryCreate['packaging']
  hasBox: boolean
  hasManual: boolean
  boxCondition: Condition | ''
  manualCondition: Condition | ''
  itemCondition: Condition | ''
  pricePaid: string
  purchasedAt: string
  purchasedFrom: string
  status: EntryCreate['status']
  rating: string
  notes: string
  storageLocation: string
  pinned: boolean
}

// eslint-disable-next-line react-refresh/only-export-components -- shared with ConfirmStep and the test, alongside this component.
export function defaultDetails(region: string = 'ntsc_u'): DetailsValues {
  return {
    region, edition: '', packaging: 'cib', hasBox: true, hasManual: true,
    boxCondition: '', manualCondition: '', itemCondition: '', pricePaid: '',
    purchasedAt: '', purchasedFrom: '', status: 'backlog',
    rating: '', notes: '', storageLocation: '', pinned: false,
  }
}

// detailsToCreate maps the step's values onto the shared EntryCreate
// fields. pricing_mode and match_provenance are both auto (the
// product-backed defaults); the custom path overrides pricing_mode to
// disabled and ConfirmStep overrides match_provenance to user, both
// after spreading. currency is the caller's stamp (the signed-in
// profile's currency) - this step never collects one, and stamping
// needs no rate, so it still works while conversion is down.
// eslint-disable-next-line react-refresh/only-export-components -- shared with ConfirmStep and the test, alongside this component.
export function detailsToCreate(d: DetailsValues, currency: string): Omit<EntryCreate, 'product_id' | 'display_name' | 'item_type' | 'platform_name' | 'first_release_date'> {
  return {
    media_type: 'physical',
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
  // The product identity the heading names: the canonical name plus
  // any localization bundles off the search result. The heading
  // follows the currently selected region through regionTitle, so a
  // JP copy reads by its JP identity while the select sits on ntsc_j.
  product: { name: string; localizations?: LocalizationBundle[] }
  // The picked platform's own region set (game picks with known
  // regions only): renders the Region select grouped, that set first.
  // Guidance, not enforcement - every region stays selectable.
  regionGroup?: { platformName: string; regions: EntryRegion[] }
  // Label only: the price-paid field is stamped with this at create
  // time (detailsToCreate takes it separately), never edited here.
  currency: string
  // Seeds the form when a caller remounts this step with values it
  // already collected (e.g. wizard Back from a later step); omitted,
  // the step starts blank as before.
  initialValues?: DetailsValues
  // Manual price match (game catalog path only): current choice plus
  // change callback; the row renders only when the callback is given.
  // The custom and hardware paths pass neither.
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
  const set = <K extends keyof DetailsValues>(key: K, value: DetailsValues[K]) =>
    setV((prev) => ({ ...prev, [key]: value }))
  // Packaging implies the flags: loose is by definition unboxed, while
  // cib and sealed come boxed with a manual. The gated condition
  // selects follow the flags; either can still be corrected by hand.
  const setPackaging = (packaging: DetailsValues['packaging']) =>
    setV((prev) =>
      packaging === 'loose'
        ? { ...prev, packaging, hasBox: false, hasManual: false }
        : { ...prev, packaging, hasBox: true, hasManual: true },
    )

  // The heading's identity follows the live-selected region, so a
  // region change (e.g. off a JP default) re-derives it on every render.
  const form = titleFormFor(i18n.locale)
  const title = regionTitle(product.name, product.localizations, v.region, form)
  const titleText = title.text

  const group = regionGroup && regionGroup.regions.length > 0 ? regionGroup : undefined
  const conditionSelect = (label: string, key: 'boxCondition' | 'manualCondition' | 'itemCondition') => (
    <label className={labelClass}>
      {label}
      <select value={v[key]} onChange={(e) => set(key, e.target.value as DetailsValues[typeof key])} className={inputClass}>
        <option value="">{t`Not graded`}</option>
        {CONDITIONS.map((c) => (
          <option key={c} value={c}>
            {c.replace('_', ' ')}
          </option>
        ))}
      </select>
    </label>
  )

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
        <div className="flex flex-wrap gap-3">
          <RegionPicker value={v.region} onChange={(region) => set('region', region)} regionGroup={group} required />
          <label className={labelClass}>
            <Trans>Edition or variant</Trans>
            <input value={v.edition} onChange={(e) => set('edition', e.target.value)} placeholder={t`first print, players choice...`} className={inputClass} />
          </label>
          <label className={labelClass}>
            <Trans>Packaging</Trans>
            <select value={v.packaging} onChange={(e) => setPackaging(e.target.value as DetailsValues['packaging'])} className={inputClass}>
              {PACKAGINGS.map((p) => (
                <option key={p} value={p}>
                  {p}
                </option>
              ))}
            </select>
          </label>
          {conditionSelect(t`Item condition`, 'itemCondition')}
          <label className="flex items-center gap-2 text-sm font-medium">
            <input type="checkbox" checked={v.hasBox} onChange={(e) => set('hasBox', e.target.checked)} />
            <Trans>Has box</Trans>
          </label>
          {v.hasBox && conditionSelect(t`Box condition`, 'boxCondition')}
          <label className="flex items-center gap-2 text-sm font-medium">
            <input type="checkbox" checked={v.hasManual} onChange={(e) => set('hasManual', e.target.checked)} />
            <Trans>Has manual</Trans>
          </label>
          {v.hasManual && conditionSelect(t`Manual condition`, 'manualCondition')}
        </div>
        <div className="flex flex-wrap gap-3">
          <label className={labelClass}>
            <Trans>Price paid ({currency})</Trans>
            <input inputMode="decimal" value={v.pricePaid} onChange={(e) => set('pricePaid', e.target.value)} placeholder={t`59.99`} className={inputClass} />
          </label>
          <label className={labelClass}>
            <Trans>Purchased on</Trans>
            <input type="date" value={v.purchasedAt} onChange={(e) => set('purchasedAt', e.target.value)} className={inputClass} />
          </label>
          <label className={labelClass}>
            <Trans>Purchased from</Trans>
            <input value={v.purchasedFrom} onChange={(e) => set('purchasedFrom', e.target.value)} className={inputClass} />
          </label>
          {onManualMatchChange && (
            <div className={labelClass}>
              <Trans>Price listing match (optional)</Trans>
              {manualMatch ? (
                <span className="flex items-center gap-2 font-normal">
                  {manualMatch.name}
                  <button
                    type="button"
                    onClick={() => onManualMatchChange(null)}
                    className="rounded border border-gray-300 px-2 py-0.5 text-xs hover:bg-gray-50"
                  >
                    <Trans>Clear</Trans>
                  </button>
                </span>
              ) : (
                <span className="flex items-center gap-2">
                  <button
                    type="button"
                    onClick={() => setMatchOpen(true)}
                    className="rounded border border-gray-300 px-2 py-1 text-sm hover:bg-gray-50"
                  >
                    <Trans>Match manually</Trans>
                  </button>
                  <span className="text-xs font-normal text-gray-500"><Trans>Otherwise auto-match picks the listing.</Trans></span>
                </span>
              )}
            </div>
          )}
        </div>
        <div className="flex flex-wrap gap-3">
          <label className={labelClass}>
            <Trans>Status</Trans>
            <select value={v.status} onChange={(e) => set('status', e.target.value as DetailsValues['status'])} className={inputClass}>
              {STATUSES.map((s) => (
                <option key={s} value={s}>
                  {s}
                </option>
              ))}
            </select>
          </label>
          <label className={labelClass}>
            <Trans>Rating</Trans>
            <select value={v.rating} onChange={(e) => set('rating', e.target.value)} className={inputClass}>
              <option value="">{t`Unrated`}</option>
              {Array.from({ length: 10 }, (_, i) => String(i + 1)).map((n) => (
                <option key={n} value={n}>
                  {n}
                </option>
              ))}
            </select>
          </label>
          <label className={labelClass}>
            <Trans>Storage location</Trans>
            <input value={v.storageLocation} onChange={(e) => set('storageLocation', e.target.value)} className={inputClass} />
          </label>
          <label className="flex items-center gap-2 text-sm font-medium">
            <input type="checkbox" checked={v.pinned} onChange={(e) => set('pinned', e.target.checked)} />
            <Trans>Pinned</Trans>
          </label>
        </div>
        <label className={labelClass}>
          <Trans>Notes</Trans>
          <textarea value={v.notes} onChange={(e) => set('notes', e.target.value)} rows={2} className={inputClass} />
        </label>
        <div className="flex gap-2">
          <button type="button" onClick={onBack} className="rounded border border-gray-300 px-3 py-1 text-sm hover:bg-gray-50">
            <Trans>Back</Trans>
          </button>
          <button type="submit" className="rounded bg-gray-900 px-4 py-1 text-sm text-white hover:bg-gray-700">
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
