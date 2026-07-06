import type { components } from './schema'
import { getJSON, sendJSON } from './client'

export type SearchResults = components['schemas']['SearchResults']
export type SearchResult = components['schemas']['SearchResult']
export type Product = components['schemas']['Product']
export type ResolveRequest = components['schemas']['ResolveRequest']
export type ScoreResponse = components['schemas']['ScoreResponse']
export type Recommendation = components['schemas']['Recommendation']

export function searchCatalog(type: 'game' | 'hardware', q: string): Promise<SearchResults> {
  const params = new URLSearchParams({ type, q })
  return getJSON<SearchResults>(`/api/search?${params.toString()}`)
}

export function resolveProduct(body: ResolveRequest): Promise<Product> {
  return sendJSON<Product>('POST', '/api/products/resolve', body)
}

export function fetchProduct(id: string): Promise<Product> {
  return getJSON<Product>(`/api/products/${id}`)
}

export function fetchRecommendations(): Promise<ScoreResponse> {
  return getJSON<ScoreResponse>('/api/recommendations')
}
