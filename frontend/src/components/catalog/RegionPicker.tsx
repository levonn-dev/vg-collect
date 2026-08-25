import { Trans, useLingui } from '@lingui/react/macro'
import { useState } from 'react'
import { EntryCreate } from '../../gen/facets'
import { inputClass, labelClass, linkButtonClass } from '../../lib/formStyles'
import { REGIONS } from '../../lib/listParams'
import type { EntryRegion } from '../../lib/productTitle'
import { regionLabels } from '../../lib/regionLabels'

const REGION_MAX = EntryCreate.properties.region.maxLength

// Mode derives from the value (a stored free-text region opens in text mode),
// plus a local flag so an empty free-text draft doesn't snap back mid-typing.
export default function RegionPicker({ value, onChange, regionGroup, required }: {
  value: string
  onChange: (v: string) => void
  regionGroup?: { platformName: string; regions: EntryRegion[] }
  // Entry-owned surfaces (DetailsStep, EntryForm) pass required since the
  // server 400s an empty region; item-facts surfaces leave it unset.
  required?: boolean
}) {
  const { t, i18n } = useLingui()
  const known = value === '' || (REGIONS as readonly string[]).includes(value)
  const [freeText, setFreeText] = useState(!known)

  if (freeText || !known) {
    return (
      <div className="flex flex-col gap-1">
        <label className={labelClass}>
          <Trans>Region</Trans>
          <input aria-label={t`Region`} value={value} maxLength={REGION_MAX} required={required}
            onChange={(e) => onChange(e.target.value)} className={inputClass} />
        </label>
        <button type="button" className={linkButtonClass}
          onClick={() => { setFreeText(false); onChange('') }}>
          <Trans>Pick a known region instead</Trans>
        </button>
      </div>
    )
  }

  const group = regionGroup && regionGroup.regions.length > 0 ? regionGroup : undefined
  const groupPlatformName = group?.platformName ?? ''
  const otherRegions = group ? REGIONS.filter((r) => !group.regions.some((g) => g === r)) : REGIONS
  const regionOption = (r: (typeof REGIONS)[number]) => (
    <option key={r} value={r}>{i18n._(regionLabels[r])}</option>
  )
  return (
    <div className="flex flex-col gap-1">
      <label className={labelClass}>
        <Trans>Region</Trans>
        <select aria-label={t`Region`} value={value} onChange={(e) => onChange(e.target.value)} required={required} className={inputClass}>
          {group ? (
            <>
              <optgroup label={t`Released on ${groupPlatformName}`}>{group.regions.map(regionOption)}</optgroup>
              <optgroup label={t`Other regions`}>
                <option value="">{t`Choose...`}</option>
                {otherRegions.map(regionOption)}
              </optgroup>
            </>
          ) : (
            <>
              <option value="">{t`Choose...`}</option>
              {REGIONS.map(regionOption)}
            </>
          )}
        </select>
      </label>
      <button type="button" className={linkButtonClass}
        onClick={() => { setFreeText(true); onChange('') }}>
        <Trans>My region isn't listed</Trans>
      </button>
    </div>
  )
}
