import { useQuery } from '@tanstack/react-query'
import { ApiError } from '../../api/client'
import type { Submission } from '../../api/submissions'
import { fetchSubmission } from '../../api/submissions'

// A 404 means "never submitted", a normal state for a custom entry, so it
// reads as data: null rather than an error every caller would special-case.
export function useSubmission(entryId: string) {
  return useQuery({
    queryKey: ['submission', entryId],
    queryFn: async (): Promise<Submission | null> => {
      try {
        return await fetchSubmission(entryId)
      } catch (e) {
        if (e instanceof ApiError && e.status === 404) return null
        throw e
      }
    },
    retry: false,
  })
}
