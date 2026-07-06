import { useState } from 'react'
import type { Entry, EntryUpdate } from '../../api/collection'
import { centsToDollars, dollarsToCents } from '../../lib/format'
import { entryToUpdate } from '../../lib/entryUpdate'
import { CONDITIONS, PACKAGINGS, REGIONS, STATUSES } from '../../lib/listParams'
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
  currency: string
  purchasedAt: string
  purchasedFrom: string
  status: Entry['status']
  rating: string
  notes: string
  storageLocation: string
  pinned: boolean
  tagIds: string[]
}

function valuesFrom(e: Entry): FormValues {
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
    currency: e.currency,
    purchasedAt: e.purchased_at ?? '',
    purchasedFrom: e.purchased_from ?? '',
    status: e.status,
    rating: e.rating === undefined ? '' : String(e.rating),
    notes: e.notes ?? '',
    storageLocation: e.storage_location ?? '',
    pinned: e.pinned,
    tagIds: e.tags.map((t) => t.id),
  }
}

// toUpdate lays the form values over the faithful PUT baseline. The
// update is a full replacement (absent optional = cleared), so fields
// this form does not render (the pricing pair) ride the baseline
// unchanged, and cleared inputs become absent fields on purpose.
function toUpdate(e: Entry, v: FormValues): EntryUpdate {
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
    currency: v.currency.trim() === '' ? 'USD' : v.currency.trim().toUpperCase(),
    purchased_at: v.purchasedAt === '' ? undefined : v.purchasedAt,
    purchased_from: v.purchasedFrom.trim() === '' ? undefined : v.purchasedFrom.trim(),
    status: v.status,
    rating: v.rating === '' ? undefined : Number(v.rating),
    notes: v.notes.trim() === '' ? undefined : v.notes,
    storage_location: v.storageLocation.trim() === '' ? undefined : v.storageLocation.trim(),
    pinned: v.pinned,
    tag_ids: v.tagIds,
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
  error: string | null
}

export default function EntryForm({ entry, onSave, saving, error }: EntryFormProps) {
  const [v, setV] = useState<FormValues>(() => valuesFrom(entry))
  const set = <K extends keyof FormValues>(key: K, value: FormValues[K]) =>
    setV((prev) => ({ ...prev, [key]: value }))
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
    <form
      onSubmit={(e) => {
        e.preventDefault()
        onSave(toUpdate(entry, v))
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
          <select value={v.packaging} onChange={(e) => set('packaging', e.target.value as Entry['packaging'])} className={selectClass}>
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
          Price paid
          <input inputMode="decimal" value={v.pricePaid} onChange={(e) => set('pricePaid', e.target.value)} placeholder="59.99" className={inputClass} />
        </label>
        <label className={labelClass}>
          Currency
          <input value={v.currency} onChange={(e) => set('currency', e.target.value)} maxLength={3} className={inputClass} />
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

      {error && (
        <p role="alert" className="rounded bg-red-50 p-3 text-sm text-red-700">
          {error}
        </p>
      )}
      <div>
        <button type="submit" disabled={saving} className="rounded bg-gray-900 px-4 py-2 text-sm text-white disabled:opacity-50">
          Save changes
        </button>
      </div>
    </form>
  )
}
