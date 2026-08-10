import { Trans, useLingui } from '@lingui/react/macro'
import { useState } from 'react'
import type { EntryCreate } from '../../api/collection'
import { REGION_PLATFORMS } from '../../lib/productTitle'
import StringListInput from '../StringListInput'
import PlatformPicker from '../catalog/PlatformPicker'
import RegionPicker from '../catalog/RegionPicker'
import type { CatalogPick } from '../catalog/SearchPicker'
import SearchPicker from '../catalog/SearchPicker'

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
  // Seeds the form when a caller remounts this step with values it
  // already collected (e.g. wizard Back from a later step); omitted,
  // the step starts blank as before.
  initialValues?: CustomValues
  // Seeds a fresh (no initialValues) form's name and type from the
  // search the caller was on before falling back to custom - e.g. a
  // hardware-tab search seeds itemType accessory. Ignored once
  // initialValues carries a real Back snapshot.
  seed?: { displayName: string; itemType: CustomValues['itemType'] }
  onBack: () => void
  onNext: (c: CustomValues) => void
}

// CustomStep collects the display facts for an item no provider lists.
// These stay user-owned and editable, unlike catalog snapshots.
export default function CustomStep({ initialValues, seed, onBack, onNext }: CustomStepProps) {
  const { t } = useLingui()
  const [v, setV] = useState<CustomValues>(() => initialValues ?? {
    displayName: seed?.displayName ?? '', itemType: seed?.itemType ?? 'game', platformName: '',
    platformIgdbId: undefined, region: '', firstReleaseDate: '', coverUrl: '',
    developers: [], publishers: [],
  })
  // Pristine until the user picks a region themselves - only then does
  // a later platform pick's suggestion stop overwriting it. A Back
  // remount that restores an already-explicit region (initialValues
  // carrying a non-empty one) counts as touched from the start: that
  // region was itself either a prior explicit pick or an earlier
  // platform default already applied, and either way a later platform
  // pick on the same remount must not clobber it.
  const [regionTouched, setRegionTouched] = useState(() => initialValues !== undefined && initialValues.region !== '')
  // Toggles the embedded SearchPicker used to prefill the form from an
  // existing catalog item (below) - kept as its own flag, not derived
  // from v, so Cancel can close the picker without touching any typed
  // field.
  const [basing, setBasing] = useState(false)
  // Bumped by applyBase to key-remount PlatformPicker and RegionPicker
  // below - see applyBase's comment for why a remount, not a prop
  // update, is what makes an externally applied base actually show up.
  const [baseGeneration, setBaseGeneration] = useState(0)
  const inputClass = 'rounded border border-gray-300 px-2 py-1 text-sm'
  const labelClass = 'flex flex-col gap-1 text-sm font-medium'
  const linkButtonClass = 'self-start text-xs text-gray-500 underline'

  // Replaces the form wholesale with the picked item's own facts - a
  // starting point the user can still edit, not a link to the catalog
  // item. pc_listing is excluded: a listing is a price source, not an
  // identity a custom item can be based on, and SearchPicker's default
  // kinds never surface one here anyway.
  //
  // Both pickers below derive their display mode from a useState
  // initializer that only runs at mount (RegionPicker: known value vs
  // free text; PlatformPicker: confirmed pick vs free-text search
  // text), so writing the new values into v alone would leave a picker
  // stuck in whatever mode it was already showing - RegionPicker's
  // escape hatch still open with the wire value hidden in text mode,
  // PlatformPicker's cached search text stale next to the new pick.
  // Bumping baseGeneration changes their key, forcing a full remount so
  // each re-derives its mode from the freshly applied values. Deriving
  // mode from value transitions instead (no remount) would trade this
  // bug for a worse one: it would snap a picker shut mid-typing any
  // time a user's own free-text entry happened to match a known value.
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
          {/* A sibling of the form below, not a child: nesting SearchPicker's
              own <form> inside this step's <form> would let a native submit
              (its Search button, or Enter in its search box) bubble up and
              fire this step's onSubmit too - Continue-by-accident on every
              picker search. */}
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
            <option value="game">game</option>
            <option value="console">console</option>
            <option value="accessory">accessory</option>
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
          <button type="button" onClick={onBack} className="rounded border border-gray-300 px-3 py-1 text-sm hover:bg-gray-50">
            <Trans>Back</Trans>
          </button>
          <button type="submit" className="rounded bg-gray-900 px-4 py-1 text-sm text-white hover:bg-gray-700">
            <Trans>Continue</Trans>
          </button>
        </div>
      </form>
    </div>
  )
}
