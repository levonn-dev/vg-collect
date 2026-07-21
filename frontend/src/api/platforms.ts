import type { components } from './schema'
import { getJSON } from './client'

export type PlatformCatalog = components['schemas']['PlatformCatalog']
export type Platform = components['schemas']['CatalogPlatform']

export function fetchPlatforms(): Promise<PlatformCatalog> {
  return getJSON<PlatformCatalog>('/api/platforms')
}
