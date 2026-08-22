import type { components } from './schema'
import { api, unwrap } from './client'

export type PlatformCatalog = components['schemas']['PlatformCatalog']
export type Platform = components['schemas']['CatalogPlatform']

export async function fetchPlatforms(): Promise<PlatformCatalog> {
  return unwrap(await api.GET('/api/platforms'))
}
