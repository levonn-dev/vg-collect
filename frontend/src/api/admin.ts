import type { components, paths } from './schema'
import type { Product } from './catalog'
import type { Submission } from './submissions'
import { api, unwrap } from './client'

export type UnmatchedProductsPage =
  paths['/api/admin/products/unmatched']['get']['responses']['200']['content']['application/json']
export type CommunityProductsPage =
  paths['/api/admin/products/community']['get']['responses']['200']['content']['application/json']
export type RefreshAccepted =
  paths['/api/admin/refresh']['post']['responses']['202']['content']['application/json']
export type RematchAccepted =
  paths['/api/admin/rematch']['post']['responses']['202']['content']['application/json']

export async function fetchUnmatchedProducts(offset = 0): Promise<UnmatchedProductsPage> {
  const params = new URLSearchParams({ offset: String(offset) })
  return unwrap(
    await api.GET('/api/admin/products/unmatched', { querySerializer: () => params.toString() }),
  )
}

export async function fetchCommunityProducts(offset = 0): Promise<CommunityProductsPage> {
  const params = new URLSearchParams({ offset: String(offset) })
  return unwrap(
    await api.GET('/api/admin/products/community', { querySerializer: () => params.toString() }),
  )
}

// null clears the mapping: the product becomes unmatched and held out
// of the nightly entry rematch until a mapping is set again.
export async function setProductMapping(productId: string, pcProductId: number | null): Promise<Product> {
  return unwrap(
    await api.PUT('/api/admin/products/{productId}/pricecharting', {
      params: { path: { productId } },
      body: { pc_product_id: pcProductId },
    }),
  )
}

export async function triggerRefresh(): Promise<RefreshAccepted> {
  return unwrap(await api.POST('/api/admin/refresh'))
}

export async function triggerRematch(): Promise<RematchAccepted> {
  return unwrap(await api.POST('/api/admin/rematch'))
}

// Only unmatched products with no entry references delete; the bff
// answers 409 product_referenced / product_matched otherwise.
export async function deleteProduct(productId: string): Promise<void> {
  return unwrap<void>(
    await api.DELETE('/api/admin/products/{productId}', { params: { path: { productId } } }),
  )
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

export async function fetchSubmissions(offset = 0): Promise<AdminSubmissionsPage> {
  const params = new URLSearchParams({ offset: String(offset) })
  return unwrap(
    await api.GET('/api/admin/submissions', { querySerializer: () => params.toString() }),
  )
}

export async function submitVerdict(submissionId: string, verdict: VerdictRequest): Promise<Submission> {
  return unwrap(
    await api.POST('/api/admin/submissions/{submissionId}/verdict', {
      params: { path: { submissionId } },
      body: verdict,
    }),
  )
}

export type ProfileCardsResponse =
  paths['/api/shared/profiles/by-ids']['get']['responses']['200']['content']['application/json']
export type ProfileCard = ProfileCardsResponse['profiles'][number]

// Batch-hydrates submitter handles for the submissions queue. Cards come
// back visibility-independent (the queue itself gates what it shows per
// card), so a submitter with no listed profile still resolves a handle.
export async function fetchProfileCards(ids: string[]): Promise<ProfileCardsResponse> {
  const params = new URLSearchParams()
  for (const id of ids) params.append('ids', id)
  return unwrap(
    await api.GET('/api/shared/profiles/by-ids', {
      params: { query: { ids } },
      querySerializer: () => params.toString(),
    }),
  )
}

export async function fetchPromoteCandidates(offset = 0, productId?: string): Promise<PromoteCandidatesPage> {
  const params = new URLSearchParams({ offset: String(offset) })
  if (productId) params.set('product_id', productId)
  return unwrap(
    await api.GET('/api/admin/products/promote-candidates', {
      querySerializer: () => params.toString(),
    }),
  )
}

export async function promoteProduct(productId: string, req: PromoteRequest): Promise<Product> {
  return unwrap(
    await api.POST('/api/admin/products/{productId}/promote', {
      params: { path: { productId } },
      body: req,
    }),
  )
}

export async function dismissPromoteCandidate(
  productId: string,
  provider: components['schemas']['DismissCandidateRequest']['provider'],
  providerId: number,
): Promise<void> {
  return unwrap<void>(
    await api.POST('/api/admin/products/{productId}/promote-candidates/dismiss', {
      params: { path: { productId } },
      body: { provider, provider_id: providerId },
    }),
  )
}

export type ResnapshotResult =
  paths['/api/admin/resnapshot']['post']['responses']['200']['content']['application/json']

// Synchronous, unlike the refresh/rematch triggers: the sweep counts
// ride the response.
export async function runResnapshot(): Promise<ResnapshotResult> {
  return unwrap(await api.POST('/api/admin/resnapshot'))
}

// The three normalize levers all return the contract's shared
// NormalizeResult schema; the alias re-exports it for them.
export type NormalizeResult = components['schemas']['NormalizeResult']

export async function normalizePlatforms(): Promise<NormalizeResult> {
  return unwrap(await api.POST('/api/admin/normalize-platforms'))
}

export async function normalizeRegions(): Promise<NormalizeResult> {
  return unwrap(await api.POST('/api/admin/normalize-regions'))
}

export async function normalizeCommunityRegions(): Promise<NormalizeResult> {
  return unwrap(await api.POST('/api/admin/normalize-community-regions'))
}
