import { Trans, useLingui } from '@lingui/react/macro'
import type { ReactNode } from 'react'
import { conditionLabels, packagingWireLabels, statusWireLabels } from '../../lib/enumLabels'
import { inputClass, labelClass } from '../../lib/formStyles'
import { EntryCreate } from '../../gen/facets'
import type { Condition, Packaging, Status } from '../../lib/listParams'
import { CONDITIONS, PACKAGINGS, STATUSES } from '../../lib/listParams'
import type { EntryRegion } from '../../lib/productTitle'
import RegionPicker from '../catalog/RegionPicker'

const RATING_MAX = EntryCreate.properties.rating.maximum

// One component shared by the wizard's details step and the entry editor, so
// they can't drift field by field. Controlled: hands back the full next value.

export interface CopyDetailsValues {
  region: string
  edition: string
  packaging: Packaging
  hasBox: boolean
  hasManual: boolean
  boxCondition: Condition | ''
  manualCondition: Condition | ''
  itemCondition: Condition | ''
  pricePaid: string
  purchasedAt: string
  purchasedFrom: string
  status: Status
  rating: string
  notes: string
  storageLocation: string
  pinned: boolean
}

interface CopyDetailsFieldsProps {
  value: CopyDetailsValues
  onChange: (next: CopyDetailsValues) => void
  // Label only: price-paid is stamped with this currency by the host's wire
  // mapper, never edited here.
  currencyLabel: string
  // Hosts word the edition field differently (variant-flavored in the
  // wizard, catalog-flavored in the editor), so both arrive pre-translated.
  editionLabel: string
  editionPlaceholder: string
  // Groups Region by a platform's own region set; absent renders the flat
  // select (wizard only).
  regionGroup?: { platformName: string; regions: EntryRegion[] }
  // Extra controls for the acquisition row; the wizard slots its
  // price-listing match block here.
  children?: ReactNode
}

// currencyLabel arrives renamed: the price-paid label interpolates it as
// {currency}, the placeholder every translation keys on.
export function CopyDetailsFields({ value, onChange, currencyLabel: currency, editionLabel, editionPlaceholder, regionGroup, children }: CopyDetailsFieldsProps) {
  const { t, i18n } = useLingui()
  const set = <K extends keyof CopyDetailsValues>(key: K, v: CopyDetailsValues[K]) =>
    onChange({ ...value, [key]: v })
  // Packaging implies the flags: loose is unboxed; cib/sealed come boxed
  // with a manual. Gated condition selects follow the flags but can be corrected.
  const setPackaging = (packaging: Packaging) =>
    onChange(
      packaging === 'loose'
        ? { ...value, packaging, hasBox: false, hasManual: false }
        : { ...value, packaging, hasBox: true, hasManual: true },
    )

  const conditionSelect = (label: string, key: 'boxCondition' | 'manualCondition' | 'itemCondition') => (
    <label className={labelClass}>
      {label}
      <select value={value[key]} onChange={(e) => set(key, e.target.value as CopyDetailsValues[typeof key])} className={inputClass}>
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
      <section aria-label={t`Physical details`} className="flex flex-wrap gap-3">
        <RegionPicker value={value.region} onChange={(region) => set('region', region)} regionGroup={regionGroup} required />
        <label className={labelClass}>
          {editionLabel}
          <input value={value.edition} onChange={(e) => set('edition', e.target.value)} placeholder={editionPlaceholder} className={inputClass} />
        </label>
        <label className={labelClass}>
          <Trans>Packaging</Trans>
          <select value={value.packaging} onChange={(e) => setPackaging(e.target.value as Packaging)} className={inputClass}>
            {PACKAGINGS.map((p) => (
              <option key={p} value={p}>
                {packagingWireLabels[p] ? i18n._(packagingWireLabels[p]) : p}
              </option>
            ))}
          </select>
        </label>
        {conditionSelect(t`Item condition`, 'itemCondition')}
        <label className="flex items-center gap-2 text-sm font-medium">
          <input type="checkbox" checked={value.hasBox} onChange={(e) => set('hasBox', e.target.checked)} />
          <Trans>Has box</Trans>
        </label>
        {value.hasBox && conditionSelect(t`Box condition`, 'boxCondition')}
        <label className="flex items-center gap-2 text-sm font-medium">
          <input type="checkbox" checked={value.hasManual} onChange={(e) => set('hasManual', e.target.checked)} />
          <Trans>Has manual</Trans>
        </label>
        {value.hasManual && conditionSelect(t`Manual condition`, 'manualCondition')}
      </section>

      <section aria-label={t`Acquisition`} className="flex flex-wrap gap-3">
        <label className={labelClass}>
          <Trans>Price paid ({currency})</Trans>
          <input inputMode="decimal" value={value.pricePaid} onChange={(e) => set('pricePaid', e.target.value)} placeholder={t`59.99`} className={inputClass} />
        </label>
        <label className={labelClass}>
          <Trans>Purchased on</Trans>
          <input type="date" value={value.purchasedAt} onChange={(e) => set('purchasedAt', e.target.value)} className={inputClass} />
        </label>
        <label className={labelClass}>
          <Trans>Purchased from</Trans>
          <input value={value.purchasedFrom} onChange={(e) => set('purchasedFrom', e.target.value)} className={inputClass} />
        </label>
        {children}
      </section>

      <section aria-label={t`Personal`} className="flex flex-wrap gap-3">
        <label className={labelClass}>
          <Trans>Status</Trans>
          <select value={value.status} onChange={(e) => set('status', e.target.value as Status)} className={inputClass}>
            {STATUSES.map((s) => (
              <option key={s} value={s}>
                {statusWireLabels[s] ? i18n._(statusWireLabels[s]) : s}
              </option>
            ))}
          </select>
        </label>
        <label className={labelClass}>
          <Trans>Rating</Trans>
          <select value={value.rating} onChange={(e) => set('rating', e.target.value)} className={inputClass}>
            <option value="">{t`Unrated`}</option>
            {Array.from({ length: RATING_MAX }, (_, i) => String(i + 1)).map((n) => (
              <option key={n} value={n}>
                {n}
              </option>
            ))}
          </select>
        </label>
        <label className={labelClass}>
          <Trans>Storage location</Trans>
          <input value={value.storageLocation} onChange={(e) => set('storageLocation', e.target.value)} className={inputClass} />
        </label>
        <label className="flex items-center gap-2 text-sm font-medium">
          <input type="checkbox" checked={value.pinned} onChange={(e) => set('pinned', e.target.checked)} />
          <Trans>Pinned</Trans>
        </label>
      </section>

      <label className={labelClass}>
        <Trans>Notes</Trans>
        <textarea value={value.notes} onChange={(e) => set('notes', e.target.value)} rows={3} className={inputClass} />
      </label>
    </>
  )
}
