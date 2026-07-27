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
import Tabs, { type Tab } from '../components/Tabs'

type AdminTab = 'mappings' | 'submissions'

const ADMIN_TABS: Tab<AdminTab>[] = [
  { key: 'mappings', label: 'Mappings' },
  { key: 'submissions', label: 'Submissions' },
]

// Admin is the role-gated console, in two tabs: Mappings (unmatched
// worklist, promote-candidates worklist, product lookup, refresh
// trigger) and Submissions (the catalog review queue, then the
// community products cleanup list below it). Layout already gates
// authentication; this page checks only the role, and the server
// enforces it regardless, so a bypassed guard yields 403 problems,
// never data.
export default function Admin() {
  const me = useQuery({ queryKey: ['me'], queryFn: fetchMe })
  const [tab, setTab] = useState<AdminTab>('mappings')
  if (me.isPending) return null
  if (me.isError || !me.data.roles.includes('admin')) return <Navigate to="/" replace />

  return (
    <main aria-label="Admin" className="py-6">
      <h2 className="mb-1 text-2xl font-bold">Admin</h2>
      <Tabs label="Admin sections" tabs={ADMIN_TABS} active={tab} onChange={setTab} className="mt-4" />
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
