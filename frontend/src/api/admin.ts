import type { paths } from './schema'
import type { Product } from './catalog'
import { getJSON, sendJSON } from './client'

export type UnmatchedProductsPage =
  paths['/api/admin/products/unmatched']['get']['responses']['200']['content']['application/json']
export type RefreshAccepted =
  paths['/api/admin/refresh']['post']['responses']['202']['content']['application/json']

export function fetchUnmatchedProducts(offset = 0): Promise<UnmatchedProductsPage> {
  const params = new URLSearchParams({ offset: String(offset) })
  return getJSON<UnmatchedProductsPage>(`/api/admin/products/unmatched?${params.toString()}`)
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
