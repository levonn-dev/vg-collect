import { Trans, useLingui } from '@lingui/react/macro'
import { useState } from 'react'
import { EntryCreate } from '../gen/facets'

const CREDIT_NAME_MAX = EntryCreate.properties.developers.items.maxLength
const CREDITS_MAX = EntryCreate.properties.developers.maxItems

// Blank rows are the caller's to drop at submit time; mid-edit emptiness is a
// normal typing state, not an error. Row cap mirrors the contract's maxItems.
export default function StringListInput({ label, addLabel, values, onChange }: {
  label: string
  addLabel: string
  values: string[]
  onChange: (v: string[]) => void
}) {
  const { t } = useLingui()
  // ids is keyed separately from values: an index key would let React reuse a
  // DOM node (and its focus) for a different value after a remove.
  // Minted from current max+1, not a ref counter/random source (unsafe during
  // a re-run render). A length mismatch means the caller replaced the whole
  // list (e.g. CustomStep's prefill); minting fresh ids there is correct.
  const [ids, setIds] = useState<number[]>(() => values.map((_, i) => i))
  if (ids.length !== values.length) {
    setIds(values.map((_, i) => i))
  }
  const add = () => {
    setIds((prev) => [...prev, prev.length === 0 ? 0 : Math.max(...prev) + 1])
    onChange([...values, ''])
  }
  const remove = (i: number) => {
    setIds((prev) => prev.filter((_, j) => j !== i))
    onChange(values.filter((_, j) => j !== i))
  }
  return (
    <fieldset className="flex flex-col gap-1">
      <legend className="text-sm font-medium">{label}</legend>
      {values.map((v, i) => {
        const n = i + 1
        return (
          <div key={ids[i]} className="flex items-center gap-2">
            <input
              aria-label={t`${label}: ${n}`}
              value={v}
              maxLength={CREDIT_NAME_MAX}
              onChange={(e) => onChange(values.map((x, j) => (j === i ? e.target.value : x)))}
              className="rounded border border-gray-300 px-2 py-1 text-sm"
            />
            <button
              type="button"
              aria-label={t`Remove ${label} ${n}`}
              onClick={() => remove(i)}
              className="text-xs text-gray-500 underline"
            >
              <Trans>Remove</Trans>
            </button>
          </div>
        )
      })}
      {values.length < CREDITS_MAX && (
        <button type="button" onClick={add} className="self-start text-xs text-gray-500 underline">
          {addLabel}
        </button>
      )}
    </fieldset>
  )
}
