import { Trans, useLingui } from '@lingui/react/macro'
import { useState } from 'react'
import type { EntryCreate } from '../../api/collection'
import { itemTypeValues } from '../../api/schema'
import { itemTypeWireLabels } from '../../lib/enumLabels'
import { btnPrimary, btnSecondary, inputClass, labelClass, linkButtonClass } from '../../lib/formStyles'
import { REGION_PLATFORMS } from '../../lib/productTitle'
import StringListInput from '../StringListInput'
import PlatformPicker from '../catalog/PlatformPicker'
import RegionPicker from '../catalog/RegionPicker'
import type { CatalogPick } from '../../lib/catalogPicks'
import SearchPicker from '../catalog/SearchPicker'

const ITEM_TYPE_VALUES = itemTypeValues

export interface CustomValues {
  displayName: string
  itemType: NonNullable<EntryCreate['item_type']>
  platformName: string
  platformIgdbId?: number
  region: string
  firstReleaseDate: string
  coverUrl: string
  developers: string[]
  publishers: string[]
}

interface CustomStepProps {
  // Seeds the form when a caller remounts with values already collected (e.g.
  // wizard Back); omitted, the step starts blank.
  initialValues?: CustomValues
  // Seeds a fresh form's name/type from the search the caller was on (e.g. a
  // hardware-tab search seeds itemType accessory); ignored once initialValues
  // carries a real Back snapshot.
  seed?: { displayName: string; itemType: CustomValues['itemType'] }
  onBack: () => void
  onNext: (c: CustomValues) => void
}

// Display facts for an item no provider lists; stay user-owned/editable,
// unlike catalog snapshots.
export default function CustomStep({ initialValues, seed, onBack, onNext }: CustomStepProps) {
  const { t, i18n } = useLingui()
  const [v, setV] = useState<CustomValues>(() => initialValues ?? {
    displayName: seed?.displayName ?? '', itemType: seed?.itemType ?? 'game', platformName: '',
    platformIgdbId: undefined, region: '', firstReleaseDate: '', coverUrl: '',
    developers: [], publishers: [],
  })
  // Pristine until the user picks a region; only then does a platform pick's
  // suggestion stop overwriting it. A Back remount restoring a non-empty
  // region counts as touched from the start, so a later platform pick can't
  // clobber it.
  const [regionTouched, setRegionTouched] = useState(() => initialValues !== undefined && initialValues.region !== '')
  // Own flag, not derived from v, so Cancel can close the picker without
  // touching any typed field.
  const [basing, setBasing] = useState(false)
  // Bumped by applyBase to key-remount PlatformPicker/RegionPicker below (see
  // applyBase for why a remount, not a prop update, is needed).
  const [baseGeneration, setBaseGeneration] = useState(0)

  // pc_listing excluded: a price source, not an identity to base an item on.
  // Both pickers derive display mode from a mount-only useState initializer,
  // so new values in v alone leave them stuck in the old mode; bumping
  // baseGeneration remounts them to re-derive it, instead of deriving mode
  // from value (which would snap a picker shut mid-typing on a text match).
  const applyBase = (p: CatalogPick) => {
    if (p.kind === 'pc_listing') return
    setRegionTouched(true)
    setBasing(false)
    setBaseGeneration((g) => g + 1)
    if (p.kind === 'game') {
      setV({
        displayName: p.name, itemType: 'game', platformName: p.platformName,
        platformIgdbId: p.platformId, region: p.suggestedRegion ?? '',
        firstReleaseDate: p.firstReleaseDate ?? '', coverUrl: p.coverUrl ?? '',
        developers: [], publishers: [],
      })
    } else if (p.kind === 'hardware') {
      // Systems = console, everything else accessory - resolveRequestFor's rule.
      setV({
        displayName: p.name, itemType: p.category === 'Systems' ? 'console' : 'accessory',
        platformName: '', platformIgdbId: undefined, region: p.suggestedRegion,
        firstReleaseDate: '', coverUrl: '',
        developers: [], publishers: [],
      })
    } else {
      setV({
        displayName: p.name, itemType: p.itemType, platformName: p.platformName ?? '',
        platformIgdbId: undefined, region: p.region ?? '', firstReleaseDate: p.firstReleaseDate ?? '',
        coverUrl: p.coverUrl ?? '',
        developers: p.developers ?? [], publishers: p.publishers ?? [],
      })
    }
  }

  return (
    <div className="flex flex-col gap-3">
      {basing ? (
        <div className="rounded border border-gray-200 p-3">
          {/* Sibling of the form below, not a child: nesting SearchPicker's
              <form> inside this step's would let a native submit bubble up
              and fire onSubmit too - Continue-by-accident on every search. */}
          <SearchPicker initialQuery={v.displayName || (seed?.displayName ?? '')} onPick={applyBase} />
          <button type="button" onClick={() => setBasing(false)} className={linkButtonClass}>
            <Trans>Cancel</Trans>
          </button>
        </div>
      ) : (
        <button type="button" onClick={() => setBasing(true)} className={linkButtonClass}>
          <Trans>Base on an existing item</Trans>
        </button>
      )}
      <form
        onSubmit={(e) => {
          e.preventDefault()
          if (v.displayName.trim() !== '') onNext({ ...v, displayName: v.displayName.trim() })
        }}
        aria-label={t`Custom item`}
        className="flex flex-col gap-3"
      >
        <h3 className="text-lg font-semibold"><Trans>Custom item</Trans></h3>
        <p className="max-w-prose text-sm text-gray-600">
          <Trans>
            For items search cannot find: reproductions, fan translations, homebrew, self-built
            hardware. A variant of a searchable item (a first print, a color variant) belongs on
            that item instead - pick it from search and note the variant in the edition field.
          </Trans>
        </p>
        <label className={labelClass}>
          <Trans>Name</Trans>
          <input value={v.displayName} onChange={(e) => setV({ ...v, displayName: e.target.value })} required className={inputClass} />
        </label>
        <label className={labelClass}>
          <Trans>Item type</Trans>
          <select
            value={v.itemType}
            onChange={(e) => setV({ ...v, itemType: e.target.value as CustomValues['itemType'] })}
            className={inputClass}
          >
            {ITEM_TYPE_VALUES.map((itemType) => (
              <option key={itemType} value={itemType}>{i18n._(itemTypeWireLabels[itemType])}</option>
            ))}
          </select>
        </label>
        <PlatformPicker
          key={`platform-${baseGeneration}`}
          value={{ platformIgdbId: v.platformIgdbId, platformName: v.platformName }}
          onChange={(pv) => {
            setV((prev) => {
              const suggested = pv.platformIgdbId !== undefined ? REGION_PLATFORMS[pv.platformIgdbId] : undefined
              const region = !regionTouched && suggested !== undefined ? suggested : prev.region
              return { ...prev, platformName: pv.platformName, platformIgdbId: pv.platformIgdbId, region }
            })
          }}
        />
        <RegionPicker key={`region-${baseGeneration}`} value={v.region} onChange={(region) => { setRegionTouched(true); setV({ ...v, region }) }} />
        <label className={labelClass}>
          <Trans>Release date</Trans>
          <input type="date" value={v.firstReleaseDate} onChange={(e) => setV({ ...v, firstReleaseDate: e.target.value })} className={inputClass} />
        </label>
        <StringListInput label={t`Developers`} addLabel={t`Add developer`}
          values={v.developers} onChange={(developers) => setV({ ...v, developers })} />
        <StringListInput label={t`Publishers`} addLabel={t`Add publisher`}
          values={v.publishers} onChange={(publishers) => setV({ ...v, publishers })} />
        <label className={labelClass}>
          <Trans>Cover image link (optional)</Trans>
          <input
            value={v.coverUrl}
            onChange={(e) => setV({ ...v, coverUrl: e.target.value })}
            placeholder={t`https://...`}
            className={inputClass}
          />
        </label>
        <div className="flex gap-2">
          <button type="button" onClick={onBack} className={btnSecondary}>
            <Trans>Back</Trans>
          </button>
          <button type="submit" className={btnPrimary}>
            <Trans>Continue</Trans>
          </button>
        </div>
      </form>
    </div>
  )
}
