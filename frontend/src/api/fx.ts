import type { paths } from './schema'
import { getJSON } from './client'

export type FXRates = paths['/api/fx']['get']['responses']['200']['content']['application/json']

export function fetchFxRates(): Promise<FXRates> {
  return getJSON<FXRates>('/api/fx')
}
