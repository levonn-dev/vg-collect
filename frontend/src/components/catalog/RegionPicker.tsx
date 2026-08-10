import { Trans, useLingui } from '@lingui/react/macro'
import { useState } from 'react'
import type { EntryRegion } from '../../lib/productTitle'
import { REGIONS } from '../../lib/listParams'
import { regionLabels } from '../../lib/regionLabels'

// RegionPicker is PlatformPicker's sibling for the open-world region:
// the known values as a labeled select, free text behind an
// explicit escape hatch. Mode derives from the value (a stored
// free-text region opens in text mode), plus a local flag so an empty
// free-text draft does not snap back to the select mid-typing.
export default function RegionPicker({ value, onChange, regionGroup, required }: {
  value: string
  onChange: (v: string) => void
  regionGroup?: { platformName: string; regions: EntryRegion[] }
  // Entry-owned surfaces (DetailsStep, EntryForm) must not submit an
  // empty region - the server 400s it - so they pass required; the
  // item-facts surfaces (CustomStep's base draft, ReviewPanel's mint
  // form) leave this unset because an empty region is legitimate there.
  required?: boolean
}) {
  const { t, i18n } = useLingui()
  const known = value === '' || (REGIONS as readonly string[]).includes(value)
  const [freeText, setFreeText] = useState(!known)
  const inputClass = 'rounded border border-gray-300 px-2 py-1 text-sm'
  const labelClass = 'flex flex-col gap-1 text-sm font-medium'
  const linkButtonClass = 'self-start text-xs text-gray-500 underline'

  if (freeText || !known) {
    return (
      <div className="flex flex-col gap-1">
        <label className={labelClass}>
          <Trans>Region</Trans>
          <input aria-label={t`Region`} value={value} maxLength={32} required={required}
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
