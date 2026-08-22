import type { components, paths } from './schema'
import { api, unwrap } from './client'

export type SearchResults = components['schemas']['SearchResults']
export type SearchResult = components['schemas']['SearchResult']
export type Product = components['schemas']['Product']
export type ResolveRequest = components['schemas']['ResolveRequest']
export type ScoreResponse = components['schemas']['ScoreResponse']
export type Recommendation = components['schemas']['Recommendation']

export type SearchKind = paths['/api/search']['get']['parameters']['query']['type']

export async function searchCatalog(type: SearchKind, q: string): Promise<SearchResults> {
  const params = new URLSearchParams({ type, q })
  return unwrap(
    await api.GET('/api/search', {
      params: { query: { type, q } },
      querySerializer: () => params.toString(),
    }),
  )
}

export async function resolveProduct(body: ResolveRequest): Promise<Product> {
  return unwrap(await api.POST('/api/products/resolve', { body }))
}

export async function fetchProduct(id: string): Promise<Product> {
  return unwrap(await api.GET('/api/products/{productId}', { params: { path: { productId: id } } }))
}

export async function fetchRecommendations(): Promise<ScoreResponse> {
  return unwrap(await api.GET('/api/recommendations'))
}
