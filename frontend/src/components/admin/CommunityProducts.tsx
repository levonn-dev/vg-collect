import { Trans, useLingui } from '@lingui/react/macro'
import { msg, t } from '@lingui/core/macro'
import type { I18n, MessageDescriptor } from '@lingui/core'
import { useInfiniteQuery, useMutation, useQueryClient } from '@tanstack/react-query'
import { useState } from 'react'
import { deleteProduct, fetchCommunityProducts } from '../../api/admin'
import { confirmThen } from '../../lib/confirm'
import { formatDate, releaseYear } from '../../lib/format'
import { btnSecondaryXs } from '../../lib/formStyles'
import { offsetNextPageParam } from '../../lib/pagination'
import { refetchWarning, renderQueryState } from '../../lib/queryBoundary'
import { regionLabelText } from '../../lib/regionLabels'
import { resolveApiError } from '../../lib/resolveApiError'
import ItemTypeIcon from '../ItemTypeIcon'
import LoadMoreButton from '../LoadMoreButton'

const deleteErrorCodes: Record<string, MessageDescriptor> = {
  product_referenced: msg`In use by existing entries - cannot delete.`,
}

// This file's own strings use the explicit t(i18n) form (not the
// useLingui()-bound t) so they match resolveApiError's own
// explicit-i18n signature without importing a second, same-named t.
function deleteErrorMessage(e: unknown, i18n: I18n): string {
  return resolveApiError(e, i18n, deleteErrorCodes, msg`The product could not be deleted.`)
}

// CommunityProducts lists every admin-minted, un-promoted community
// product, for cleanup. Delete reuses the guarded admin delete
// (deleteProduct): an unreferenced product goes away immediately (the
// list invalidates and the row disappears on refetch); one still
// referenced by entries answers 409 product_referenced, shown inline
// on that row only, so a single blocked row never hides the rest of
// the list.
export default function CommunityProducts() {
  const { i18n } = useLingui()
  const queryClient = useQueryClient()
  const [blocked, setBlocked] = useState<Record<string, unknown>>({})
  const list = useInfiniteQuery({
    queryKey: ['admin', 'community'],
    queryFn: ({ pageParam }) => fetchCommunityProducts(pageParam),
    initialPageParam: 0,
    getNextPageParam: (last, pages) => offsetNextPageParam(last, pages, (p) => p.products.length),
  })

  const clearBlocked = (productId: string) =>
    setBlocked((prev) => {
      if (!(productId in prev)) return prev
      const next = { ...prev }
      delete next[productId]
      return next
    })

  const del = useMutation({
    mutationFn: (productId: string) => deleteProduct(productId),
    onSuccess: (_data, productId) => {
      clearBlocked(productId)
      void queryClient.invalidateQueries({ queryKey: ['admin'] })
    },
    onError: (e, productId) => setBlocked((prev) => ({ ...prev, [productId]: e })),
  })

  const remove = (productId: string) =>
    confirmThen(
      t(i18n)`Delete this product from the catalog? Only products that no entries reference can be deleted.`,
      () => {
        clearBlocked(productId)
        del.mutate(productId)
      },
    )

  if (list.isPending || (list.isError && list.data === undefined)) {
    return renderQueryState(list, {
      size: 'subsection',
      className: 'mt-4',
      role: 'alert',
      loading: <Trans>Loading community products...</Trans>,
      error: <Trans>Community products could not be loaded.</Trans>,
    })
  }

  const products = list.data.pages.flatMap((p) => p.products)

  return (
    <section aria-label={t(i18n)`Community products`} className="mt-6">
      <h3 className="text-base font-semibold"><Trans>Community products</Trans></h3>
      <p className="mt-1 text-sm text-gray-500">
        <Trans>Community catalog products; delete removes unused ones - products referenced by entries are protected.</Trans>
      </p>
      {refetchWarning(list)}
      <table className="mt-2 w-full text-left text-sm">
        <thead>
          <tr className="border-b border-gray-200 text-gray-500">
            <th className="py-1 pr-2 font-normal"><Trans>Name</Trans></th>
            <th className="py-1 pr-2 font-normal"><Trans>Platform</Trans></th>
            <th className="py-1 pr-2 font-normal"><Trans>Region</Trans></th>
            <th className="py-1 pr-2 font-normal"><Trans>Release</Trans></th>
            <th className="py-1 pr-2 font-normal"><Trans>Updated</Trans></th>
            <th className="py-1 font-normal"></th>
          </tr>
        </thead>
        <tbody>
          {products.map((p) => (
            <tr key={p.id} className="border-b border-gray-100">
              <td className="py-1 pr-2">
                <div className="flex items-center gap-2">
                  {p.community?.cover_url ? (
                    <img src={p.community.cover_url} alt="" className="h-10 w-auto rounded" />
                  ) : (
                    <div
                      aria-hidden="true"
                      className="flex h-10 w-8 shrink-0 items-center justify-center rounded bg-gray-100 text-gray-400"
                    >
                      <ItemTypeIcon type={p.type} className="h-5 w-5" />
                    </div>
                  )}
                  {p.name}
                </div>
              </td>
              <td className="py-1 pr-2">{p.community?.platform_name ?? ''}</td>
              <td className="py-1 pr-2">{p.community?.region ? regionLabelText(i18n, p.community.region) : ''}</td>
              <td className="py-1 pr-2">{releaseYear(p.community?.first_release_date) ?? ''}</td>
              <td className="py-1 pr-2">{formatDate(p.updated_at)}</td>
              <td className="py-1">
                <button
                  type="button"
                  onClick={() => remove(p.id)}
                  disabled={del.isPending && del.variables === p.id}
                  className={btnSecondaryXs}
                >
                  <Trans>Delete</Trans>
                </button>
                {p.id in blocked && (
                  <p role="alert" className="mt-1 text-xs text-red-700">
                    {deleteErrorMessage(blocked[p.id], i18n)}
                  </p>
                )}
              </td>
            </tr>
          ))}
        </tbody>
      </table>
      <LoadMoreButton query={list} className="mt-2" />
    </section>
  )
}
