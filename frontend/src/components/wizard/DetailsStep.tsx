import { useState } from 'react'
import type { EntryCreate } from '../../api/collection'
import type { ManualMatch } from '../../lib/catalog'
import { dollarsToCents } from '../../lib/format'
import { CONDITIONS, PACKAGINGS, REGIONS, STATUSES } from '../../lib/listParams'
import ManualMatchPicker from './ManualMatchPicker'

type Condition = NonNullable<EntryCreate['item_condition']>

export interface DetailsValues {
  region: EntryCreate['region']
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
export function defaultDetails(): DetailsValues {
  return {
    region: 'ntsc_u', edition: '', packaging: 'cib', hasBox: true, hasManual: true,
    boxCondition: '', manualCondition: '', itemCondition: '', pricePaid: '',
    purchasedAt: '', purchasedFrom: '', status: 'backlog',
    rating: '', notes: '', storageLocation: '', pinned: false,
  }
}

// detailsToCreate maps the step's values onto the shared EntryCreate
// fields. pricing_mode is auto (the product-backed default); the
// custom path overrides it to disabled after spreading. currency is
// the caller's stamp (the signed-in profile's currency) - this step
// never collects one, and stamping needs no rate, so it still works
// while conversion is down.
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
    status: d.status,
    rating: d.rating === '' ? undefined : Number(d.rating),
    notes: d.notes.trim() === '' ? undefined : d.notes,
    storage_location: d.storageLocation.trim() === '' ? undefined : d.storageLocation.trim(),
    pinned: d.pinned,
  }
}

interface DetailsStepProps {
  heading: string
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

export default function DetailsStep({ heading, currency, initialValues, manualMatch, onManualMatchChange, manualMatchQuery, onBack, onNext }: DetailsStepProps) {
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

  const inputClass = 'rounded border border-gray-300 px-2 py-1 text-sm'
  const labelClass = 'flex flex-col gap-1 text-sm font-medium'
  const conditionSelect = (label: string, key: 'boxCondition' | 'manualCondition' | 'itemCondition') => (
    <label className={labelClass}>
      {label}
      <select value={v[key]} onChange={(e) => set(key, e.target.value as DetailsValues[typeof key])} className={inputClass}>
        <option value="">Not graded</option>
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
        aria-label="Copy details"
        className="flex flex-col gap-4"
      >
        <h3 className="text-lg font-semibold">{heading}</h3>
        <div className="flex flex-wrap gap-3">
          <label className={labelClass}>
            Region
            <select value={v.region} onChange={(e) => set('region', e.target.value as DetailsValues['region'])} className={inputClass}>
              {REGIONS.map((r) => (
                <option key={r} value={r}>
                  {r.replace('_', '-').toUpperCase()}
                </option>
              ))}
            </select>
          </label>
          <label className={labelClass}>
            Edition or variant
            <input value={v.edition} onChange={(e) => set('edition', e.target.value)} placeholder="first print, players choice..." className={inputClass} />
          </label>
          <label className={labelClass}>
            Packaging
            <select value={v.packaging} onChange={(e) => setPackaging(e.target.value as DetailsValues['packaging'])} className={inputClass}>
              {PACKAGINGS.map((p) => (
                <option key={p} value={p}>
                  {p}
                </option>
              ))}
            </select>
          </label>
          {conditionSelect('Item condition', 'itemCondition')}
          <label className="flex items-center gap-2 text-sm font-medium">
            <input type="checkbox" checked={v.hasBox} onChange={(e) => set('hasBox', e.target.checked)} />
            Has box
          </label>
          {v.hasBox && conditionSelect('Box condition', 'boxCondition')}
          <label className="flex items-center gap-2 text-sm font-medium">
            <input type="checkbox" checked={v.hasManual} onChange={(e) => set('hasManual', e.target.checked)} />
            Has manual
          </label>
          {v.hasManual && conditionSelect('Manual condition', 'manualCondition')}
        </div>
        <div className="flex flex-wrap gap-3">
          <label className={labelClass}>
            Price paid ({currency})
            <input inputMode="decimal" value={v.pricePaid} onChange={(e) => set('pricePaid', e.target.value)} placeholder="59.99" className={inputClass} />
          </label>
          <label className={labelClass}>
            Purchased on
            <input type="date" value={v.purchasedAt} onChange={(e) => set('purchasedAt', e.target.value)} className={inputClass} />
          </label>
          <label className={labelClass}>
            Purchased from
            <input value={v.purchasedFrom} onChange={(e) => set('purchasedFrom', e.target.value)} className={inputClass} />
          </label>
          {onManualMatchChange && (
            <div className={labelClass}>
              Price listing match (optional)
              {manualMatch ? (
                <span className="flex items-center gap-2 font-normal">
                  {manualMatch.name}
                  <button
                    type="button"
                    onClick={() => onManualMatchChange(null)}
                    className="rounded border border-gray-300 px-2 py-0.5 text-xs hover:bg-gray-50"
                  >
                    Clear
                  </button>
                </span>
              ) : (
                <span className="flex items-center gap-2">
                  <button
                    type="button"
                    onClick={() => setMatchOpen(true)}
                    className="rounded border border-gray-300 px-2 py-1 text-sm hover:bg-gray-50"
                  >
                    Match manually
                  </button>
                  <span className="text-xs font-normal text-gray-500">Otherwise auto-match picks the listing.</span>
                </span>
              )}
            </div>
          )}
        </div>
        <div className="flex flex-wrap gap-3">
          <label className={labelClass}>
            Status
            <select value={v.status} onChange={(e) => set('status', e.target.value as DetailsValues['status'])} className={inputClass}>
              {STATUSES.map((s) => (
                <option key={s} value={s}>
                  {s}
                </option>
              ))}
            </select>
          </label>
          <label className={labelClass}>
            Rating
            <select value={v.rating} onChange={(e) => set('rating', e.target.value)} className={inputClass}>
              <option value="">Unrated</option>
              {Array.from({ length: 10 }, (_, i) => String(i + 1)).map((n) => (
                <option key={n} value={n}>
                  {n}
                </option>
              ))}
            </select>
          </label>
          <label className={labelClass}>
            Storage location
            <input value={v.storageLocation} onChange={(e) => set('storageLocation', e.target.value)} className={inputClass} />
          </label>
          <label className="flex items-center gap-2 text-sm font-medium">
            <input type="checkbox" checked={v.pinned} onChange={(e) => set('pinned', e.target.checked)} />
            Pinned
          </label>
        </div>
        <label className={labelClass}>
          Notes
          <textarea value={v.notes} onChange={(e) => set('notes', e.target.value)} rows={2} className={inputClass} />
        </label>
        <div className="flex gap-2">
          <button type="button" onClick={onBack} className="rounded border border-gray-300 px-3 py-1 text-sm hover:bg-gray-50">
            Back
          </button>
          <button type="submit" className="rounded bg-gray-900 px-4 py-1 text-sm text-white hover:bg-gray-700">
            Continue
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
