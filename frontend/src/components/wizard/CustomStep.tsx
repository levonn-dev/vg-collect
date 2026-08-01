import { Trans, useLingui } from '@lingui/react/macro'
import { useState } from 'react'
import type { EntryCreate } from '../../api/collection'
import PlatformPicker from '../catalog/PlatformPicker'

export interface CustomValues {
  displayName: string
  itemType: NonNullable<EntryCreate['item_type']>
  platformName: string
  platformIgdbId?: number
  firstReleaseDate: string
  coverUrl: string
}

interface CustomStepProps {
  // Seeds the form when a caller remounts this step with values it
  // already collected (e.g. wizard Back from a later step); omitted,
  // the step starts blank as before.
  initialValues?: CustomValues
  onBack: () => void
  onNext: (c: CustomValues) => void
}

// CustomStep collects the display facts for an item no provider lists.
// These stay user-owned and editable, unlike catalog snapshots.
export default function CustomStep({ initialValues, onBack, onNext }: CustomStepProps) {
  const { t } = useLingui()
  const [v, setV] = useState<CustomValues>(() => initialValues ?? {
    displayName: '', itemType: 'game', platformName: '', platformIgdbId: undefined, firstReleaseDate: '', coverUrl: '',
  })
  const inputClass = 'rounded border border-gray-300 px-2 py-1 text-sm'
  const labelClass = 'flex flex-col gap-1 text-sm font-medium'

  return (
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
        value={{ platformIgdbId: v.platformIgdbId, platformName: v.platformName }}
        onChange={(pv) => setV({ ...v, platformName: pv.platformName, platformIgdbId: pv.platformIgdbId })}
      />
      <label className={labelClass}>
        <Trans>Release date</Trans>
        <input type="date" value={v.firstReleaseDate} onChange={(e) => setV({ ...v, firstReleaseDate: e.target.value })} className={inputClass} />
      </label>
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
  )
}
