import { Trans, useLingui } from '@lingui/react/macro'
import { useState } from 'react'
import type { Entry, EntryUpdate } from '../../api/collection'
import { entryToUpdate } from '../../lib/entryUpdate'
import { centsToDollars, dollarsToCents, enteredCentsToUsdCents, usdCentsToMajor } from '../../lib/format'
import { btnPrimary, inputClass, labelClass } from '../../lib/formStyles'
import { useDisplayMoney } from '../../lib/useDisplayMoney'
import PlatformPicker from '../catalog/PlatformPicker'
import type { CopyDetailsValues } from './CopyDetailsFields'
import { CopyDetailsFields } from './CopyDetailsFields'
import type { PricingValue } from './PricingPanel'
import PricingPanel from './PricingPanel'
import TagPicker from './TagPicker'

// Shared copy-details cluster plus what only the editor collects: custom-entry
// display fields, tags, and the pricing draft.
interface FormValues extends CopyDetailsValues {
  displayName: string
  platformName: string
  platformIgdbId?: number
  firstReleaseDate: string
  coverUrl: string
  tagIds: string[]
  pricing: PricingValue
}

// Stored pair in the input currency shows verbatim (edit-side twin of the pin
// rule); anything else converts the USD snapshot into that currency.
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

// Full-replacement PUT: absent optional = cleared, so cleared inputs become
// absent fields on purpose. currency rides the baseline untouched (stamped
// once at create); the custom-price pair's baseline is overridden only when
// the draft holds a convertible value, so an empty draft can't clobber it.
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

interface EntryFormProps {
  entry: Entry
  onSave: (update: EntryUpdate) => void
  saving: boolean
  saved: boolean
  error: string | null
}

export default function EntryForm({ entry, onSave, saving, saved, error }: EntryFormProps) {
  const { t } = useLingui()
  const money = useDisplayMoney()
  // Input currency freezes per mount; a rate snapshot arriving mid-edit
  // must not silently reinterpret typed text.
  const [inputCurrency] = useState(() => (money.ready ? money.currency : 'USD'))
  const [v, setV] = useState<FormValues>(() => valuesFrom(entry, inputCurrency, money.rateFor(inputCurrency)))
  // Saved confirmation must disappear the moment the form drifts from what
  // was saved, so every field change flips this.
  const [editedSinceSave, setEditedSinceSave] = useState(false)
  // Client-side save blocker (a proxy needs a chosen source); any further edit retracts it.
  const [pricingError, setPricingError] = useState<string | null>(null)
  const set = <K extends keyof FormValues>(key: K, value: FormValues[K]) => {
    setEditedSinceSave(true)
    setPricingError(null)
    setV((prev) => ({ ...prev, [key]: value }))
  }
  // Shared cluster hands back one full next value; same drift bookkeeping applies.
  const setDetails = (next: CopyDetailsValues) => {
    setEditedSinceSave(true)
    setPricingError(null)
    setV((prev) => ({ ...prev, ...next }))
  }
  const custom = !entry.product_id
  const currency = entry.currency

  return (
    <>
      {/* Above the form: forms can't nest, and the proxy picker embeds a
          catalog search form. Draft lives here, so pricing saves with it. */}
      <PricingPanel entry={entry} value={v.pricing} onChange={(p) => set('pricing', p)} inputCurrency={inputCurrency} />
    <form
      onSubmit={(e) => {
        e.preventDefault()
        // Read fresh at submit: input currency froze at mount but its rate
        // must not - a header switch mid-edit (optimistic ['me'] update, no
        // remount) can move it.
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

      <CopyDetailsFields
        value={v}
        onChange={setDetails}
        currencyLabel={currency}
        editionLabel={t`Edition`}
        editionPlaceholder={t`first print, black label...`}
      />

      <TagPicker value={v.tagIds} onChange={(ids) => set('tagIds', ids)} />

      {(pricingError ?? error) && (
        <p role="alert" className="rounded bg-red-50 p-3 text-sm text-red-700">
          {pricingError ?? error}
        </p>
      )}
      <div className="flex items-center gap-3">
        <button type="submit" disabled={saving} className={btnPrimary}>
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
