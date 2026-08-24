import { Trans, useLingui } from '@lingui/react/macro'
import { useState } from 'react'
import { EntryCreate } from '../gen/facets'

const CREDIT_NAME_MAX = EntryCreate.properties.developers.items.maxLength
const CREDITS_MAX = EntryCreate.properties.developers.maxItems

// StringListInput edits an ordered list of short text values (credit
// company names): one text input per element with its own remove
// button, plus an add button appending an empty row. Blank rows are
// the caller's to drop at submit time - mid-edit emptiness is a
// normal typing state, not an error. The row cap mirrors the
// contract's maxItems on credit lists.
export default function StringListInput({ label, addLabel, values, onChange }: {
  label: string
  addLabel: string
  values: string[]
  onChange: (v: string[]) => void
}) {
  const { t } = useLingui()
  // Row identity lives here, keyed separately from `values` itself, so
  // removing one row does not make React reuse the next row's DOM node
  // (and its just-clicked focus) for a different value - a position
  // (index) key would silently retarget the Remove button onto the
  // wrong credit. IDs are minted from the current id set itself (one
  // past its running max), not a ref counter or a random source: React
  // may re-run a render, and neither a ref write nor a non-deterministic
  // draw is safe to do there. A length mismatch against `values` means
  // the caller replaced the whole list itself (e.g. CustomStep's
  // applyBase prefill) rather than going through add/remove below;
  // every row is new either way, so minting a fresh id set for the new
  // length is correct, not a loss of identity.
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
