import { useQuery } from '@tanstack/react-query'
import { fetchMe } from '../api/me'

// Every consumer reads the same ['me'] cache entry through this hook,
// so a single invalidation refreshes all of them.
export function useMe() {
  return useQuery({ queryKey: ['me'], queryFn: fetchMe })
}
