import { Trans, useLingui } from '@lingui/react/macro'
import { msg } from '@lingui/core/macro'
import type { MessageDescriptor } from '@lingui/core'
import { useState } from 'react'
import type { Entry, EntryUpdate } from '../../api/collection'
import { centsToDollars, dollarsToCents, enteredCentsToUsdCents, usdCentsToMajor } from '../../lib/format'
import { entryToUpdate } from '../../lib/entryUpdate'
import { CONDITIONS, PACKAGINGS, REGIONS, STATUSES } from '../../lib/listParams'
import { useDisplayMoney } from '../../lib/useDisplayMoney'
import PlatformPicker from '../catalog/PlatformPicker'
import type { PricingValue } from './PricingPanel'
import PricingPanel from './PricingPanel'
import TagPicker from './TagPicker'

type Condition = NonNullable<Entry['item_condition']>

interface FormValues {
  displayName: string
  platformName: string
  platformIgdbId?: number
  firstReleaseDate: string
  coverUrl: string
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
    platformIgdbId: e.platform?.igdb_platform_id,
    firstReleaseDate: e.first_release_date ?? '',
    coverUrl: e.cover_url ?? '',
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
    u.platform_igdb_id = v.platformIgdbId
    u.first_release_date = v.firstReleaseDate === '' ? undefined : v.firstReleaseDate
    u.cover_url = v.coverUrl.trim() === '' ? undefined : v.coverUrl.trim()
  }
  return u
}

// Same source text as FilterBar.tsx's chipLabels condition/region
// entries (a filter chip and a form field can both need the same
// word); duplicate msgids merge into one catalog entry.
const conditionLabels: Record<Condition, MessageDescriptor> = {
  mint: msg`Mint`,
  near_mint: msg`Near mint`,
  very_good: msg`Very good`,
  good: msg`Good`,
  acceptable: msg`Acceptable`,
  poor: msg`Poor`,
}

const regionLabels: Record<Entry['region'], MessageDescriptor> = {
  ntsc_u: msg`NTSC-U`,
  ntsc_j: msg`NTSC-J`,
  pal: msg`PAL`,
  region_free: msg`Region free`,
}

// Identity-preserving: these two selects have never been prettified,
// so the option text stays the raw wire value (unlike condition/region
// above). The table only exists so the value enters the catalog; an
// unknown future wire value falls back to rendering itself raw.
const packagingLabels: Record<string, MessageDescriptor> = {
  sealed: msg`sealed`,
  cib: msg`cib`,
  loose: msg`loose`,
}

const statusLabels: Record<string, MessageDescriptor> = {
  backlog: msg`backlog`,
  playing: msg`playing`,
  beaten: msg`beaten`,
  completed: msg`completed`,
  dropped: msg`dropped`,
  shelved: msg`shelved`,
}

interface EntryFormProps {
  entry: Entry
  onSave: (update: EntryUpdate) => void
  saving: boolean
  saved: boolean
  error: string | null
}

export default function EntryForm({ entry, onSave, saving, saved, error }: EntryFormProps) {
  const { t, i18n } = useLingui()
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
        <option value="">{t`Not graded`}</option>
        {CONDITIONS.map((c) => (
          <option key={c} value={c}>
            {i18n._(conditionLabels[c])}
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
          setPricingError(t`Choose a price source before saving.`)
          return
        }
        if (v.pricing.mode === 'custom' && dollarsToCents(v.pricing.customValue) === undefined) {
          setPricingError(t`Enter a custom price before saving.`)
          return
        }
        if (v.pricing.mode === 'custom' && inputCurrency !== 'USD' && submitRate === undefined) {
          setPricingError(t`Exchange rates are unavailable; try saving again shortly.`)
          return
        }
        setEditedSinceSave(false)
        onSave(toUpdate(entry, v, inputCurrency, submitRate))
      }}
      className="flex flex-col gap-4"
      aria-label={t`Entry editor`}
    >
      {custom && (
        <section aria-label={t`Custom item`} className="flex flex-col gap-3">
          <label className={labelClass}>
            <Trans>Name</Trans>
            <input value={v.displayName} onChange={(e) => set('displayName', e.target.value)} required className={inputClass} />
          </label>
          <div className="flex gap-3">
            <PlatformPicker
              value={{ platformIgdbId: v.platformIgdbId, platformName: v.platformName }}
              onChange={(pv) => { set('platformName', pv.platformName); set('platformIgdbId', pv.platformIgdbId) }}
            />
            <label className={labelClass}>
              <Trans>Release date</Trans>
              <input type="date" value={v.firstReleaseDate} onChange={(e) => set('firstReleaseDate', e.target.value)} className={inputClass} />
            </label>
          </div>
          <label className={labelClass}>
            <Trans>Cover image link (optional)</Trans>
            <input
              value={v.coverUrl}
              onChange={(e) => set('coverUrl', e.target.value)}
              placeholder={t`https://...`}
              className={inputClass}
            />
          </label>
        </section>
      )}

      <section aria-label={t`Physical details`} className="flex flex-wrap gap-3">
        <label className={labelClass}>
          <Trans>Region</Trans>
          <select value={v.region} onChange={(e) => set('region', e.target.value as Entry['region'])} className={selectClass}>
            {REGIONS.map((r) => (
              <option key={r} value={r}>
                {i18n._(regionLabels[r])}
              </option>
            ))}
          </select>
        </label>
        <label className={labelClass}>
          <Trans>Edition</Trans>
          <input value={v.edition} onChange={(e) => set('edition', e.target.value)} placeholder={t`first print, black label...`} className={inputClass} />
        </label>
        <label className={labelClass}>
          <Trans>Packaging</Trans>
          <select value={v.packaging} onChange={(e) => setPackaging(e.target.value as Entry['packaging'])} className={selectClass}>
            {PACKAGINGS.map((p) => (
              <option key={p} value={p}>
                {packagingLabels[p] ? i18n._(packagingLabels[p]) : p}
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
      </section>

      <section aria-label={t`Acquisition`} className="flex flex-wrap gap-3">
        <label className={labelClass}>
          <Trans>Price paid ({entry.currency})</Trans>
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
      </section>

      <section aria-label={t`Personal`} className="flex flex-wrap gap-3">
        <label className={labelClass}>
          <Trans>Status</Trans>
          <select value={v.status} onChange={(e) => set('status', e.target.value as Entry['status'])} className={selectClass}>
            {STATUSES.map((s) => (
              <option key={s} value={s}>
                {statusLabels[s] ? i18n._(statusLabels[s]) : s}
              </option>
            ))}
          </select>
        </label>
        <label className={labelClass}>
          <Trans>Rating</Trans>
          <select value={v.rating} onChange={(e) => set('rating', e.target.value)} className={selectClass}>
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
      </section>

      <label className={labelClass}>
        <Trans>Notes</Trans>
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
          <Trans>Save changes</Trans>
        </button>
        <span aria-live="polite" className="text-sm text-green-800">
          {saved && !editedSinceSave && !saving ? <Trans>Saved.</Trans> : ''}
        </span>
      </div>
    </form>
    </>
  )
}
