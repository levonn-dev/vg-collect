import { useQuery } from '@tanstack/react-query'
import { fetchMe } from '../api/client'

// useMe centralizes the session-identity query: the app shell, the
// public shell's session probe, and every component that needs the
// signed-in profile (currency, handle, roles, comment authorship) all
// read the same ['me'] cache entry through this one hook, so a single
// invalidation refreshes every consumer. Models the same one-hook
// shape as useFxRates (lib/useDisplayMoney.ts) for its sibling query.
export function useMe() {
  return useQuery({ queryKey: ['me'], queryFn: fetchMe })
}
