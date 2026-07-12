import { useState } from 'react'
import type { Entry, EntryUpdate } from '../../api/collection'
import { centsToDollars, dollarsToCents, enteredCentsToUsdCents, usdCentsToMajor } from '../../lib/format'
import { entryToUpdate } from '../../lib/entryUpdate'
import { CONDITIONS, PACKAGINGS, REGIONS, STATUSES } from '../../lib/listParams'
import { useDisplayMoney } from '../../lib/useDisplayMoney'
import type { PricingValue } from './PricingPanel'
import PricingPanel from './PricingPanel'
import TagPicker from './TagPicker'

type Condition = NonNullable<Entry['item_condition']>

interface FormValues {
  displayName: string
  platformName: string
  firstReleaseDate: string
  region: Entry['region']
  edition: string
  packaging: Entry['packaging']
  hasBox: boolean
  hasManual: boolean
  boxCondition: Condition | ''
  manualCondition: Condition | ''
  itemCondition: Condition | ''
  pricePaid: string
  purchasedAt: string
  purchasedFrom: string
  status: Entry['status']
  rating: string
  notes: string
  storageLocation: string
  pinned: boolean
  tagIds: string[]
  pricing: PricingValue
}

// customValueDraft seeds the custom-price text: a stored pair in the
// input currency is shown verbatim (the pin rule's edit-side twin);
// anything else converts the USD snapshot into the input currency.
function customValueDraft(e: Entry, inputCurrency: string, rate: number | undefined): string {
  if (e.custom_value_entered_cents !== undefined && e.custom_value_entered_currency === inputCurrency) {
    return centsToDollars(e.custom_value_entered_cents)
  }
  if (e.custom_value_cents === undefined) return ''
  if (inputCurrency === 'USD' || rate === undefined) return centsToDollars(e.custom_value_cents)
  return usdCentsToMajor(e.custom_value_cents, rate).toFixed(2)
}

function valuesFrom(e: Entry, inputCurrency: string, rate: number | undefined): FormValues {
  return {
    displayName: e.display_name,
    platformName: e.platform?.name ?? '',
    firstReleaseDate: e.first_release_date ?? '',
    region: e.region,
    edition: e.edition ?? '',
    packaging: e.packaging,
    hasBox: e.has_box,
    hasManual: e.has_manual,
    boxCondition: e.box_condition ?? '',
    manualCondition: e.manual_condition ?? '',
    itemCondition: e.item_condition ?? '',
    pricePaid: centsToDollars(e.price_paid_cents),
    purchasedAt: e.purchased_at ?? '',
    purchasedFrom: e.purchased_from ?? '',
    status: e.status,
    rating: e.rating === undefined ? '' : String(e.rating),
    notes: e.notes ?? '',
    storageLocation: e.storage_location ?? '',
    pinned: e.pinned,
    tagIds: e.tags.map((t) => t.id),
    pricing: {
      mode: e.pricing_mode,
      productId: e.pricing_product_id,
      customValue: customValueDraft(e, inputCurrency, rate),
    },
  }
}

// toUpdate lays the form values over the faithful PUT baseline. The
// update is a full replacement (absent optional = cleared), so
// cleared inputs become absent fields on purpose. Two fields ride the
// baseline through untouched instead: currency is stamped once at
// create and never re-currencied by an edit, so entryToUpdate(e)'s
// `currency: e.currency` is left standing with no override here; and
// the custom-price pair, whose baseline is only overridden below when
// the draft holds a convertible value - an empty or unconvertible
// draft must not clobber stored memory just because some unrelated
// field changed.
function toUpdate(e: Entry, v: FormValues, inputCurrency: string, rate: number | undefined): EntryUpdate {
  const u: EntryUpdate = {
    ...entryToUpdate(e),
    region: v.region,
    edition: v.edition.trim() === '' ? undefined : v.edition.trim(),
    packaging: v.packaging,
    has_box: v.hasBox,
    has_manual: v.hasManual,
    box_condition: v.hasBox && v.boxCondition !== '' ? v.boxCondition : undefined,
    manual_condition: v.hasManual && v.manualCondition !== '' ? v.manualCondition : undefined,
    item_condition: v.itemCondition === '' ? undefined : v.itemCondition,
    price_paid_cents: dollarsToCents(v.pricePaid),
    purchased_at: v.purchasedAt === '' ? undefined : v.purchasedAt,
    purchased_from: v.purchasedFrom.trim() === '' ? undefined : v.purchasedFrom.trim(),
    pricing_mode: v.pricing.mode,
    pricing_product_id: v.pricing.productId,
    status: v.status,
    rating: v.rating === '' ? undefined : Number(v.rating),
    notes: v.notes.trim() === '' ? undefined : v.notes,
    storage_location: v.storageLocation.trim() === '' ? undefined : v.storageLocation.trim(),
    pinned: v.pinned,
    tag_ids: v.tagIds,
  }
  const draftCents = dollarsToCents(v.pricing.customValue)
  if (draftCents !== undefined && (inputCurrency === 'USD' || rate !== undefined)) {
    u.custom_value_cents = inputCurrency === 'USD' ? draftCents : enteredCentsToUsdCents(draftCents, rate!)
    u.custom_value_entered_cents = draftCents
    u.custom_value_entered_currency = inputCurrency
  }
  if (!e.product_id) {
    u.display_name = v.displayName.trim()
    u.platform_name = v.platformName.trim() === '' ? undefined : v.platformName.trim()
    u.first_release_date = v.firstReleaseDate === '' ? undefined : v.firstReleaseDate
  }
  return u
}

const conditionLabels: Record<Condition, string> = {
  mint: 'Mint',
  near_mint: 'Near mint',
  very_good: 'Very good',
  good: 'Good',
  acceptable: 'Acceptable',
  poor: 'Poor',
}

const regionLabels: Record<Entry['region'], string> = {
  ntsc_u: 'NTSC-U',
  ntsc_j: 'NTSC-J',
  pal: 'PAL',
  region_free: 'Region free',
}

interface EntryFormProps {
  entry: Entry
  onSave: (update: EntryUpdate) => void
  saving: boolean
  saved: boolean
  error: string | null
}

export default function EntryForm({ entry, onSave, saving, saved, error }: EntryFormProps) {
  const money = useDisplayMoney()
  // The input currency freezes per mount: a rate snapshot arriving
  // mid-edit must not silently reinterpret typed text.
  const [inputCurrency] = useState(() => (money.ready ? money.currency : 'USD'))
  const [v, setV] = useState<FormValues>(() => valuesFrom(entry, inputCurrency, money.rateFor(inputCurrency)))
  // The saved confirmation must disappear the moment the form drifts
  // from what was saved, so every field change flips this.
  const [editedSinceSave, setEditedSinceSave] = useState(false)
  // Client-side save blocker (a proxy needs a chosen source); any
  // further edit retracts it.
  const [pricingError, setPricingError] = useState<string | null>(null)
  const set = <K extends keyof FormValues>(key: K, value: FormValues[K]) => {
    setEditedSinceSave(true)
    setPricingError(null)
    setV((prev) => ({ ...prev, [key]: value }))
  }
  // Packaging implies the flags: loose is by definition unboxed, while
  // cib and sealed come boxed with a manual. The gated condition
  // selects follow the flags; either can still be corrected by hand.
  const setPackaging = (packaging: Entry['packaging']) => {
    setEditedSinceSave(true)
    setPricingError(null)
    setV((prev) =>
      packaging === 'loose'
        ? { ...prev, packaging, hasBox: false, hasManual: false }
        : { ...prev, packaging, hasBox: true, hasManual: true },
    )
  }
  const custom = !entry.product_id

  const selectClass = 'rounded border border-gray-300 px-2 py-1 text-sm'
  const inputClass = 'rounded border border-gray-300 px-2 py-1 text-sm'
  const labelClass = 'flex flex-col gap-1 text-sm font-medium'

  const conditionSelect = (
    label: string,
    key: 'boxCondition' | 'manualCondition' | 'itemCondition',
  ) => (
    <label className={labelClass}>
      {label}
      <select value={v[key]} onChange={(e) => set(key, e.target.value as FormValues[typeof key])} className={selectClass}>
        <option value="">Not graded</option>
        {CONDITIONS.map((c) => (
          <option key={c} value={c}>
            {conditionLabels[c]}
          </option>
        ))}
      </select>
    </label>
  )

  return (
    <>
      {/* Above the form element, not inside it: the proxy picker
          embeds the catalog search form, and forms cannot nest. The
          draft still lives here, so pricing edits save with the same
          button as everything else. */}
      <PricingPanel entry={entry} value={v.pricing} onChange={(p) => set('pricing', p)} inputCurrency={inputCurrency} />
    <form
      onSubmit={(e) => {
        e.preventDefault()
        // Read fresh at submit: the input currency froze at mount, but
        // its rate must not - a header switch mid-edit (optimistic
        // ['me'] update, no remount) can move the CURRENT display
        // currency's rate without touching the frozen input currency's.
        const submitRate = money.rateFor(inputCurrency)
        if (v.pricing.mode === 'proxy' && !v.pricing.productId) {
          setPricingError('Choose a price source before saving.')
          return
        }
        if (v.pricing.mode === 'custom' && dollarsToCents(v.pricing.customValue) === undefined) {
          setPricingError('Enter a custom price before saving.')
          return
        }
        if (v.pricing.mode === 'custom' && inputCurrency !== 'USD' && submitRate === undefined) {
          setPricingError('Exchange rates are unavailable; try saving again shortly.')
          return
        }
        setEditedSinceSave(false)
        onSave(toUpdate(entry, v, inputCurrency, submitRate))
      }}
      className="flex flex-col gap-4"
      aria-label="Entry editor"
    >
      {custom && (
        <section aria-label="Custom item" className="flex flex-col gap-3">
          <label className={labelClass}>
            Name
            <input value={v.displayName} onChange={(e) => set('displayName', e.target.value)} required className={inputClass} />
          </label>
          <div className="flex gap-3">
            <label className={labelClass}>
              Platform
              <input value={v.platformName} onChange={(e) => set('platformName', e.target.value)} className={inputClass} />
            </label>
            <label className={labelClass}>
              Release date
              <input type="date" value={v.firstReleaseDate} onChange={(e) => set('firstReleaseDate', e.target.value)} className={inputClass} />
            </label>
          </div>
        </section>
      )}

      <section aria-label="Physical details" className="flex flex-wrap gap-3">
        <label className={labelClass}>
          Region
          <select value={v.region} onChange={(e) => set('region', e.target.value as Entry['region'])} className={selectClass}>
            {REGIONS.map((r) => (
              <option key={r} value={r}>
                {regionLabels[r]}
              </option>
            ))}
          </select>
        </label>
        <label className={labelClass}>
          Edition
          <input value={v.edition} onChange={(e) => set('edition', e.target.value)} placeholder="first print, black label..." className={inputClass} />
        </label>
        <label className={labelClass}>
          Packaging
          <select value={v.packaging} onChange={(e) => setPackaging(e.target.value as Entry['packaging'])} className={selectClass}>
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
      </section>

      <section aria-label="Acquisition" className="flex flex-wrap gap-3">
        <label className={labelClass}>
          Price paid ({entry.currency})
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
      </section>

      <section aria-label="Personal" className="flex flex-wrap gap-3">
        <label className={labelClass}>
          Status
          <select value={v.status} onChange={(e) => set('status', e.target.value as Entry['status'])} className={selectClass}>
            {STATUSES.map((s) => (
              <option key={s} value={s}>
                {s}
              </option>
            ))}
          </select>
        </label>
        <label className={labelClass}>
          Rating
          <select value={v.rating} onChange={(e) => set('rating', e.target.value)} className={selectClass}>
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
      </section>

      <label className={labelClass}>
        Notes
        <textarea value={v.notes} onChange={(e) => set('notes', e.target.value)} rows={3} className={inputClass} />
      </label>

      <TagPicker value={v.tagIds} onChange={(ids) => set('tagIds', ids)} />

      {(pricingError ?? error) && (
        <p role="alert" className="rounded bg-red-50 p-3 text-sm text-red-700">
          {pricingError ?? error}
        </p>
      )}
      <div className="flex items-center gap-3">
        <button type="submit" disabled={saving} className="rounded bg-gray-900 px-4 py-2 text-sm text-white enabled:hover:bg-gray-700 disabled:opacity-50">
          Save changes
        </button>
        <span aria-live="polite" className="text-sm text-green-800">
          {saved && !editedSinceSave && !saving ? 'Saved.' : ''}
        </span>
      </div>
    </form>
    </>
  )
}
