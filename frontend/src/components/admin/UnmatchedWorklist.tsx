import { useInfiniteQuery, useQueryClient } from '@tanstack/react-query'
import { useState } from 'react'
import { fetchUnmatchedProducts } from '../../api/admin'
import type { Product } from '../../api/catalog'
import MappingFix from './MappingFix'

// UnmatchedWorklist pages through every product with no mapping
// (held ones flagged). Fixing a row changes the list itself, so a
// successful fix invalidates admin queries and loaded pages refetch
// with their stored pageParam (rows that got fixed leave the list).
export default function UnmatchedWorklist() {
  const queryClient = useQueryClient()
  const [fixing, setFixing] = useState<Product | null>(null)
  const list = useInfiniteQuery({
    queryKey: ['admin', 'unmatched'],
    queryFn: ({ pageParam }) => fetchUnmatchedProducts(pageParam),
    initialPageParam: 0,
    getNextPageParam: (last, pages) => {
      const loaded = pages.reduce((n, p) => n + p.products.length, 0)
      return loaded < last.total_count ? loaded : undefined
    },
  })

  const done = () => {
    setFixing(null)
    void queryClient.invalidateQueries({ queryKey: ['admin'] })
  }

  if (list.isPending) return <p className="mt-4 text-sm text-gray-500">Loading worklist...</p>
  if (list.isError)
    return (
      <p role="alert" className="mt-4 text-sm text-red-700">
        The worklist could not be loaded.
      </p>
    )

  const products = list.data.pages.flatMap((p) => p.products)
  const total = list.data.pages[0].total_count

  return (
    <section aria-label="Unmatched products" className="mt-6">
      <h3 className="text-base font-semibold">{total} unmatched products</h3>
      <table className="mt-2 w-full text-left text-sm">
        <thead>
          <tr className="border-b border-gray-200 text-gray-500">
            <th className="py-1 pr-2 font-normal">Name</th>
            <th className="py-1 pr-2 font-normal">Type</th>
            <th className="py-1 pr-2 font-normal">Platform / console</th>
            <th className="py-1 pr-2 font-normal">Region / edition / variant</th>
            <th className="py-1 pr-2 font-normal">Updated</th>
            <th className="py-1 font-normal"></th>
          </tr>
        </thead>
        <tbody>
          {products.map((p) => (
            <tr key={p.id} className="border-b border-gray-100">
              <td className="py-1 pr-2">
                {p.name}
                {p.match_hold && (
                  <span className="ml-2 rounded bg-amber-100 px-1.5 py-0.5 text-xs font-semibold text-amber-800">
                    held
                  </span>
                )}
              </td>
              <td className="py-1 pr-2">{p.type}</td>
              <td className="py-1 pr-2">{p.platform?.name ?? ''}</td>
              <td className="py-1 pr-2">{[p.region, p.edition, p.variant].filter(Boolean).join(' / ')}</td>
              <td className="py-1 pr-2">{p.updated_at.slice(0, 10)}</td>
              <td className="py-1">
                <button
                  type="button"
                  onClick={() => setFixing(p)}
                  className="rounded border border-gray-300 px-2 py-0.5 text-xs hover:bg-gray-50"
                >
                  Fix mapping
                </button>
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
          Load more
        </button>
      )}
      {fixing && <MappingFix product={fixing} onDone={done} />}
    </section>
  )
}
