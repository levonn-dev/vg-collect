import { Trans, useLingui } from '@lingui/react/macro'
import { msg } from '@lingui/core/macro'
import type { MessageDescriptor } from '@lingui/core'
import { useQuery } from '@tanstack/react-query'
import { useState } from 'react'
import { Navigate } from 'react-router'
import { fetchMe } from '../api/client'
import CommunityProducts from '../components/admin/CommunityProducts'
import ProductLookup from '../components/admin/ProductLookup'
import PromoteCandidates from '../components/admin/PromoteCandidates'
import RefreshTrigger from '../components/admin/RefreshTrigger'
import RematchTrigger from '../components/admin/RematchTrigger'
import SubmissionsQueue from '../components/admin/SubmissionsQueue'
import UnmatchedWorklist from '../components/admin/UnmatchedWorklist'
import Tabs, { type Tab } from '../components/Tabs'

type AdminTab = 'mappings' | 'submissions'

// Tabs.tsx renders whatever label string each caller hands it (no i18n
// awareness of its own); the table stays msg descriptors at module
// scope (same shape as Explore's SORT_TABS / Feed's FEED_TABS) and gets
// resolved into the plain strings Tab<T> expects down in the component
// body, where i18n is available.
const ADMIN_TABS: { key: AdminTab; label: MessageDescriptor }[] = [
  { key: 'mappings', label: msg`Mappings` },
  { key: 'submissions', label: msg`Submissions` },
]

// Admin is the role-gated console, in two tabs: Mappings (unmatched
// worklist, promote-candidates worklist, product lookup, refresh
// trigger, entry rematch trigger) and Submissions (the catalog review
// queue, then the community products cleanup list below it). Layout
// already gates authentication; this page checks only the role, and
// the server enforces it regardless, so a bypassed guard yields 403
// problems, never data.
export default function Admin() {
  const { t, i18n } = useLingui()
  const me = useQuery({ queryKey: ['me'], queryFn: fetchMe })
  const [tab, setTab] = useState<AdminTab>('mappings')
  if (me.isPending) return null
  if (me.isError || !me.data.roles.includes('admin')) return <Navigate to="/" replace />

  return (
    <main aria-label={t`Admin`} className="py-6">
      <h2 className="mb-1 text-2xl font-bold"><Trans>Admin</Trans></h2>
      <Tabs
        label={t`Admin sections`}
        tabs={ADMIN_TABS.map((s): Tab<AdminTab> => ({ key: s.key, label: i18n._(s.label) }))}
        active={tab}
        onChange={setTab}
        className="mt-4"
      />
      {tab === 'mappings' ? (
        <>
          <UnmatchedWorklist />
          <PromoteCandidates />
          <ProductLookup />
          <RefreshTrigger />
          <RematchTrigger />
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
