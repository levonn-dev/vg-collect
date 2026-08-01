import { Trans, useLingui } from '@lingui/react/macro'
import { t } from '@lingui/core/macro'
import type { I18n } from '@lingui/core'
import { useInfiniteQuery, useMutation, useQueryClient } from '@tanstack/react-query'
import { useState } from 'react'
import { deleteProduct, fetchCommunityProducts } from '../../api/admin'
import { ApiError } from '../../api/client'
import { releaseYear } from '../../lib/format'
import ItemTypeIcon from '../ItemTypeIcon'

// t(i18n) throughout this file, component included: deleteErrorMessage
// is a plain function (cannot call useLingui() itself), so it takes the
// caller's i18n explicitly; the component uses the same explicit form
// for its own strings rather than importing a second, same-named t.
function deleteErrorMessage(e: unknown, i18n: I18n): string {
  if (e instanceof ApiError) {
    if (e.code === 'product_referenced') return t(i18n)`In use by existing entries - cannot delete.`
    if (e.message) return e.message
  }
  return t(i18n)`The product could not be deleted.`
}

// CommunityProducts lists every admin-minted, un-promoted community
// product, for cleanup. Delete reuses the guarded admin delete
// (deleteProduct, from the guarded-delete round): an unreferenced
// product goes away immediately (the list invalidates and the row
// disappears on refetch); one still referenced by entries answers 409
// product_referenced, shown inline on that row only, so a single
// blocked row never hides the rest of the list.
export default function CommunityProducts() {
  const { i18n } = useLingui()
  const queryClient = useQueryClient()
  const [blocked, setBlocked] = useState<Record<string, string>>({})
  const list = useInfiniteQuery({
    queryKey: ['admin', 'community'],
    queryFn: ({ pageParam }) => fetchCommunityProducts(pageParam),
    initialPageParam: 0,
    getNextPageParam: (last, pages) => {
      const loaded = pages.reduce((n, p) => n + p.products.length, 0)
      return loaded < last.total_count ? loaded : undefined
    },
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
    onError: (e, productId) => setBlocked((prev) => ({ ...prev, [productId]: deleteErrorMessage(e, i18n) })),
  })

  const remove = (productId: string) => {
    clearBlocked(productId)
    del.mutate(productId)
  }

  if (list.isPending) return <p className="mt-4 text-sm text-gray-500"><Trans>Loading community products...</Trans></p>
  if (list.isError)
    return (
      <p role="alert" className="mt-4 text-sm text-red-700">
        <Trans>Community products could not be loaded.</Trans>
      </p>
    )

  const products = list.data.pages.flatMap((p) => p.products)

  return (
    <section aria-label={t(i18n)`Community products`} className="mt-6">
      <h3 className="text-base font-semibold"><Trans>Community products</Trans></h3>
      <p className="mt-1 text-sm text-gray-500">
        <Trans>Community catalog products; delete removes unused ones - products referenced by entries are protected.</Trans>
      </p>
      <table className="mt-2 w-full text-left text-sm">
        <thead>
          <tr className="border-b border-gray-200 text-gray-500">
            <th className="py-1 pr-2 font-normal"><Trans>Name</Trans></th>
            <th className="py-1 pr-2 font-normal"><Trans>Platform</Trans></th>
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
              <td className="py-1 pr-2">{releaseYear(p.community?.first_release_date) ?? ''}</td>
              <td className="py-1 pr-2">{p.updated_at.slice(0, 10)}</td>
              <td className="py-1">
                <button
                  type="button"
                  onClick={() => remove(p.id)}
                  disabled={del.isPending && del.variables === p.id}
                  className="rounded border border-gray-300 px-2 py-0.5 text-xs hover:bg-gray-50 disabled:opacity-50"
                >
                  <Trans>Delete</Trans>
                </button>
                {blocked[p.id] && (
                  <p role="alert" className="mt-1 text-xs text-red-700">
                    {blocked[p.id]}
                  </p>
                )}
              </td>
            </tr>
          ))}
        </tbody>
      </table>
      {list.hasNextPage && (
        <button
          type="button"
          onClick={() => void list.fetchNextPage()}
          disabled={list.isFetchingNextPage}
          className="mt-2 rounded border border-gray-300 px-3 py-1 text-sm hover:bg-gray-50 disabled:opacity-50"
        >
          <Trans>Load more</Trans>
        </button>
      )}
    </section>
  )
}
