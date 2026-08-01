import { Trans, useLingui } from '@lingui/react/macro'
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { useState } from 'react'
import { ApiError } from '../../api/client'
import { createTag, fetchTags } from '../../api/collection'

interface TagPickerProps {
  value: string[]
  onChange: (ids: string[]) => void
}

// TagPicker assigns existing tags and creates new ones inline. It only
// edits the id list; the surrounding form submits it as tag_ids.
export default function TagPicker({ value, onChange }: TagPickerProps) {
  const { t } = useLingui()
  const queryClient = useQueryClient()
  const tags = useQuery({ queryKey: ['tags'], queryFn: fetchTags })
  const [name, setName] = useState('')
  const create = useMutation({
    mutationFn: createTag,
    onSuccess: (tag) => {
      setName('')
      onChange([...value, tag.id])
      void queryClient.invalidateQueries({ queryKey: ['tags'] })
    },
  })

  const toggle = (id: string) => {
    onChange(value.includes(id) ? value.filter((v) => v !== id) : [...value, id])
  }

  return (
    <fieldset>
      <legend className="text-sm font-medium"><Trans>Tags</Trans></legend>
      {tags.isPending && <p className="text-xs text-gray-500"><Trans>Loading tags...</Trans></p>}
      {tags.isError && (
        <p role="alert" className="text-xs text-red-700">
          <Trans>Tags cannot be loaded right now.</Trans>
        </p>
      )}
      <div className="mt-1 flex flex-wrap gap-3">
        {tags.data?.map((t) => (
          <label key={t.id} className="flex items-center gap-1 text-sm">
            <input type="checkbox" checked={value.includes(t.id)} onChange={() => toggle(t.id)} />
            {t.name}
          </label>
        ))}
      </div>
      <div className="mt-2 flex items-center gap-2">
        <input
          aria-label={t`New tag`}
          value={name}
          onChange={(e) => setName(e.target.value)}
          placeholder={t`New tag`}
          className="rounded border border-gray-300 px-2 py-1 text-sm"
        />
        <button
          type="button"
          onClick={() => name.trim() && create.mutate(name.trim())}
          disabled={create.isPending || name.trim() === ''}
          className="rounded border border-gray-300 px-2 py-1 text-sm enabled:hover:bg-gray-50 disabled:opacity-50"
        >
          <Trans>Add tag</Trans>
        </button>
      </div>
      {create.isError && (
        <p role="alert" className="mt-1 text-xs text-red-700">
          {create.error instanceof ApiError && create.error.message
            ? create.error.message
            : t`The tag could not be created.`}
        </p>
      )}
    </fieldset>
  )
}
