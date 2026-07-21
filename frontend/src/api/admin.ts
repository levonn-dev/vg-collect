import type { paths } from './schema'
import type { Product } from './catalog'
import type { Submission } from './submissions'
import { getJSON, sendJSON } from './client'

export type UnmatchedProductsPage =
  paths['/api/admin/products/unmatched']['get']['responses']['200']['content']['application/json']
export type CommunityProductsPage =
  paths['/api/admin/products/community']['get']['responses']['200']['content']['application/json']
export type RefreshAccepted =
  paths['/api/admin/refresh']['post']['responses']['202']['content']['application/json']

export function fetchUnmatchedProducts(offset = 0): Promise<UnmatchedProductsPage> {
  const params = new URLSearchParams({ offset: String(offset) })
  return getJSON<UnmatchedProductsPage>(`/api/admin/products/unmatched?${params.toString()}`)
}

export function fetchCommunityProducts(offset = 0): Promise<CommunityProductsPage> {
  const params = new URLSearchParams({ offset: String(offset) })
  return getJSON<CommunityProductsPage>(`/api/admin/products/community?${params.toString()}`)
}

// null clears the mapping: the product becomes unmatched and held out
// of the nightly re-match walk until a mapping is set again.
export function setProductMapping(productId: string, pcProductId: number | null): Promise<Product> {
  return sendJSON<Product>('PUT', `/api/admin/products/${productId}/pricecharting`, {
    pc_product_id: pcProductId,
  })
}

export function triggerRefresh(): Promise<RefreshAccepted> {
  return sendJSON<RefreshAccepted>('POST', '/api/admin/refresh')
}

// Only unmatched products with no entry references delete; the bff
// answers 409 product_referenced / product_matched otherwise.
export function deleteProduct(productId: string): Promise<void> {
  return sendJSON<void>('DELETE', `/api/admin/products/${productId}`)
}

export type AdminSubmissionsPage =
  paths['/api/admin/submissions']['get']['responses']['200']['content']['application/json']
export type AdminSubmission = AdminSubmissionsPage['submissions'][number]
export type VerdictRequest =
  NonNullable<paths['/api/admin/submissions/{submissionId}/verdict']['post']['requestBody']>['content']['application/json']
export type PromoteCandidatesPage =
  paths['/api/admin/products/promote-candidates']['get']['responses']['200']['content']['application/json']
export type PromoteRequest =
  NonNullable<paths['/api/admin/products/{productId}/promote']['post']['requestBody']>['content']['application/json']

export function fetchSubmissions(offset = 0): Promise<AdminSubmissionsPage> {
  const params = new URLSearchParams({ offset: String(offset) })
  return getJSON<AdminSubmissionsPage>(`/api/admin/submissions?${params.toString()}`)
}

export function submitVerdict(submissionId: string, verdict: VerdictRequest): Promise<Submission> {
  return sendJSON<Submission>('POST', `/api/admin/submissions/${submissionId}/verdict`, verdict)
}

export function fetchPromoteCandidates(offset = 0, productId?: string): Promise<PromoteCandidatesPage> {
  const params = new URLSearchParams({ offset: String(offset) })
  if (productId) params.set('product_id', productId)
  return getJSON<PromoteCandidatesPage>(`/api/admin/products/promote-candidates?${params.toString()}`)
}

export function promoteProduct(productId: string, req: PromoteRequest): Promise<Product> {
  return sendJSON<Product>('POST', `/api/admin/products/${productId}/promote`, req)
}

export function dismissPromoteCandidate(
  productId: string,
  provider: 'igdb' | 'pricecharting',
  providerId: number,
): Promise<void> {
  return sendJSON<void>('POST', `/api/admin/products/${productId}/promote-candidates/dismiss`, {
    provider,
    provider_id: providerId,
  })
}
