import { useQuery, useQueryClient } from '@tanstack/react-query'
import { useState } from 'react'
import { fetchProduct } from '../../api/catalog'
import { ApiError } from '../../api/client'
import MappingFix from './MappingFix'

// ProductLookup is the remap reach: wrong mappings surface while
// looking at an entry, where the product id is at hand; pasting it
// here brings up the product regardless of matching state.
export default function ProductLookup() {
  const queryClient = useQueryClient()
  const [input, setInput] = useState('')
  const [id, setId] = useState('')
  const product = useQuery({
    queryKey: ['admin', 'product', id],
    queryFn: () => fetchProduct(id),
    enabled: id !== '',
    retry: false,
  })

  const done = () => {
    void queryClient.invalidateQueries({ queryKey: ['admin'] })
  }

  return (
    <section aria-label="Product lookup" className="mt-6">
      <h3 className="text-base font-semibold">Product lookup</h3>
      <form
        className="mt-2 flex gap-2"
        onSubmit={(e) => {
          e.preventDefault()
          setId(input.trim())
        }}
      >
        <input
          type="text"
          aria-label="Product id"
          value={input}
          onChange={(e) => setInput(e.target.value)}
          placeholder="Product id (uuid)"
          className="w-96 rounded border border-gray-300 px-2 py-1 text-sm"
        />
        <button
          type="submit"
          disabled={input.trim() === ''}
          className="rounded border border-gray-300 px-3 py-1 text-sm hover:bg-gray-50 disabled:opacity-50"
        >
          Look up
        </button>
      </form>
      {product.isError && (
        <p role="alert" className="mt-2 text-sm text-red-700">
          {product.error instanceof ApiError && product.error.status === 404
            ? 'No product with that id.'
            : 'The lookup failed.'}
        </p>
      )}
      {product.isSuccess && (
        <div className="mt-2">
          <p className="text-sm font-semibold">{product.data.name}</p>
          <p className="text-sm text-gray-500">
            {product.data.type}
            {product.data.platform ? ` - ${product.data.platform.name}` : ''}
          </p>
          <MappingFix product={product.data} onDone={done} />
        </div>
      )}
    </section>
  )
}
