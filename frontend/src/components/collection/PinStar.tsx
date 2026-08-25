import { useLingui } from '@lingui/react/macro'
import { useMutation, useQueryClient } from '@tanstack/react-query'
import type { Entry } from '../../api/collection'
import { updateEntry } from '../../api/collection'
import { entryToUpdate } from '../../lib/entryUpdate'

// Flips pinned through the full-replacement PUT: the payload is the faithful
// baseline with one field changed, so a toggle never clears anything else.
export default function PinStar({ entry }: { entry: Entry }) {
  const { t } = useLingui()
  const queryClient = useQueryClient()
  const toggle = useMutation({
    mutationFn: () => updateEntry(entry.id, { ...entryToUpdate(entry), pinned: !entry.pinned }),
    onSuccess: (updated) => {
      queryClient.setQueryData(['entry', entry.id], updated)
      void queryClient.invalidateQueries({ queryKey: ['entries'] })
    },
  })
  return (
    <button
      type="button"
      onClick={() => toggle.mutate()}
      disabled={toggle.isPending}
      aria-pressed={entry.pinned}
      aria-label={entry.pinned ? t`Unpin` : t`Pin`}
      className={entry.pinned ? 'text-amber-500' : 'text-gray-300 hover:text-amber-400'}
    >
      {'\u2605'}
    </button>
  )
}
