import { useQuery } from '@tanstack/react-query'
import { Navigate } from 'react-router'
import { fetchMe } from '../api/client'
import ProductLookup from '../components/admin/ProductLookup'
import RefreshWalk from '../components/admin/RefreshWalk'
import UnmatchedWorklist from '../components/admin/UnmatchedWorklist'

// Admin is the role-gated console: the unmatched-products worklist, a
// product lookup for remaps, and the refresh trigger. Layout already
// gates authentication; this page checks only the role, and the
// server enforces it regardless, so a bypassed guard yields 403
// problems, never data.
export default function Admin() {
  const me = useQuery({ queryKey: ['me'], queryFn: fetchMe })
  if (me.isPending) return null
  if (me.isError || !me.data.roles.includes('admin')) return <Navigate to="/" replace />

  return (
    <main aria-label="Admin" className="py-6">
      <h2 className="mb-1 text-2xl font-bold">Admin</h2>
      <UnmatchedWorklist />
      <ProductLookup />
      <RefreshWalk />
    </main>
  )
}
