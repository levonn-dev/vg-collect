import type { paths } from './schema'
import { api, unwrap } from './client'

export type FXRates = paths['/api/fx']['get']['responses']['200']['content']['application/json']

export async function fetchFxRates(): Promise<FXRates> {
  return unwrap(await api.GET('/api/fx'))
}
