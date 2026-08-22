import { Trans, useLingui } from '@lingui/react/macro'
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
  return (
    <fieldset className="flex flex-col gap-1">
      <legend className="text-sm font-medium">{label}</legend>
      {values.map((v, i) => {
        const n = i + 1
        return (
          <div key={i} className="flex items-center gap-2">
            <input
              aria-label={`${label} ${n}`}
              value={v}
              maxLength={CREDIT_NAME_MAX}
              onChange={(e) => onChange(values.map((x, j) => (j === i ? e.target.value : x)))}
              className="rounded border border-gray-300 px-2 py-1 text-sm"
            />
            <button
              type="button"
              aria-label={t`Remove ${label} ${n}`}
              onClick={() => onChange(values.filter((_, j) => j !== i))}
              className="text-xs text-gray-500 underline"
            >
              <Trans>Remove</Trans>
            </button>
          </div>
        )
      })}
      {values.length < CREDITS_MAX && (
        <button type="button" onClick={() => onChange([...values, ''])} className="self-start text-xs text-gray-500 underline">
          {addLabel}
        </button>
      )}
    </fieldset>
  )
}
