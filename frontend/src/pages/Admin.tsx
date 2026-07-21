import { useQuery } from '@tanstack/react-query'
import { useState } from 'react'
import { Navigate } from 'react-router'
import { fetchMe } from '../api/client'
import CommunityProducts from '../components/admin/CommunityProducts'
import ProductLookup from '../components/admin/ProductLookup'
import PromoteCandidates from '../components/admin/PromoteCandidates'
import RefreshWalk from '../components/admin/RefreshWalk'
import SubmissionsQueue from '../components/admin/SubmissionsQueue'
import UnmatchedWorklist from '../components/admin/UnmatchedWorklist'

// Admin is the role-gated console, in two tabs: Mappings (unmatched
// worklist, promote-candidates worklist, product lookup, refresh
// trigger) and Submissions (the catalog review queue, then the
// community products cleanup list below it). Layout already gates
// authentication; this page checks only the role, and the server
// enforces it regardless, so a bypassed guard yields 403 problems,
// never data.
export default function Admin() {
  const me = useQuery({ queryKey: ['me'], queryFn: fetchMe })
  const [tab, setTab] = useState<'mappings' | 'submissions'>('mappings')
  if (me.isPending) return null
  if (me.isError || !me.data.roles.includes('admin')) return <Navigate to="/" replace />

  return (
    <main aria-label="Admin" className="py-6">
      <h2 className="mb-1 text-2xl font-bold">Admin</h2>
      <div role="tablist" aria-label="Admin sections" className="mt-4 flex gap-1 border-b border-gray-200">
        {(['mappings', 'submissions'] as const).map((k) => (
          <button
            key={k}
            role="tab"
            aria-selected={tab === k}
            onClick={() => setTab(k)}
            className={
              tab === k
                ? 'border-b-2 border-gray-900 px-3 py-1 text-sm font-semibold'
                : 'px-3 py-1 text-sm text-gray-500 hover:text-gray-900'
            }
          >
            {k === 'mappings' ? 'Mappings' : 'Submissions'}
          </button>
        ))}
      </div>
      {tab === 'mappings' ? (
        <>
          <UnmatchedWorklist />
          <PromoteCandidates />
          <ProductLookup />
          <RefreshWalk />
        </>
      ) : (
        <>
          <SubmissionsQueue />
          <CommunityProducts />
        </>
      )}
    </main>
  )
}
