import { Trans, useLingui } from '@lingui/react/macro'
import { msg } from '@lingui/core/macro'
import type { MessageDescriptor } from '@lingui/core'
import { useState } from 'react'
import { Navigate } from 'react-router'
import { normalizeCommunityRegions, normalizeRegions, normalizePlatforms, triggerRefresh, triggerRematch } from '../api/admin'
import { useMe } from '../lib/useMe'
import { tabButtonId } from '../lib/tabs'
import { useDocumentTitle } from '../lib/useDocumentTitle'
import CommunityProducts from '../components/admin/CommunityProducts'
import NormalizeTrigger, { normalizeSuccessMessage } from '../components/admin/NormalizeTrigger'
import ProductLookup from '../components/admin/ProductLookup'
import PromoteCandidates from '../components/admin/PromoteCandidates'
import ResnapshotTrigger from '../components/admin/ResnapshotTrigger'
import SubmissionsQueue from '../components/admin/SubmissionsQueue'
import UnmatchedWorklist from '../components/admin/UnmatchedWorklist'
import Tabs, { type Tab } from '../components/Tabs'

type AdminTab = 'mappings' | 'submissions'

const MAPPINGS_PANEL = 'admin-mappings-panel'
const SUBMISSIONS_PANEL = 'admin-submissions-panel'

// Tabs.tsx has no i18n of its own, so labels stay msg descriptors at
// module scope, resolved in the component body.
const ADMIN_TABS: { key: AdminTab; label: MessageDescriptor; panelId: string }[] = [
  { key: 'mappings', label: msg`Mappings`, panelId: MAPPINGS_PANEL },
  { key: 'submissions', label: msg`Submissions`, panelId: SUBMISSIONS_PANEL },
]

// Two tabs: Mappings (worklists + Maintenance triggers) and
// Submissions (review queue + cleanup list). Layout gates
// authentication; this page checks only role, server enforces it regardless.
export default function Admin() {
  const { t, i18n } = useLingui()
  useDocumentTitle(t`Admin`)
  const me = useMe()
  const [tab, setTab] = useState<AdminTab>('mappings')
  if (me.isPending) return null
  if (me.isError || !me.data.roles.includes('admin')) return <Navigate to="/" replace />

  return (
    <main id="main-content" tabIndex={-1} aria-label={t`Admin`} className="py-6">
      <h2 className="mb-1 text-2xl font-bold"><Trans>Admin</Trans></h2>
      <Tabs
        label={t`Admin sections`}
        tabs={ADMIN_TABS.map((s): Tab<AdminTab> => ({ key: s.key, label: i18n._(s.label), panelId: s.panelId }))}
        active={tab}
        onChange={setTab}
        className="mt-4"
      />
      {tab === 'mappings' ? (
        <div role="tabpanel" id={MAPPINGS_PANEL} aria-labelledby={tabButtonId(MAPPINGS_PANEL)}>
          <UnmatchedWorklist />
          <PromoteCandidates />
          <ProductLookup />
          <section aria-label={t`Maintenance`} className="mt-8">
            <h3 className="text-base font-semibold"><Trans>Maintenance</Trans></h3>
            <div className="mt-3 grid gap-3 sm:grid-cols-2 xl:grid-cols-3">
              <NormalizeTrigger
                title={t`Catalog refresh`}
                actionLabel={t`Trigger catalog refresh`}
                mutationFn={triggerRefresh}
                successMessage={() => <Trans>Refresh started.</Trans>}
                inProgressCode="refresh_in_progress"
                inProgressMessage={<Trans>A refresh is already running.</Trans>}
                failureMessage={<Trans>The trigger failed - try again.</Trans>}
              />
              <NormalizeTrigger
                title={t`Entry rematch`}
                actionLabel={t`Trigger entry rematch`}
                mutationFn={triggerRematch}
                successMessage={() => <Trans>Rematch started.</Trans>}
                inProgressCode="rematch_in_progress"
                inProgressMessage={<Trans>A rematch is already running.</Trans>}
                failureMessage={<Trans>The rematch trigger failed - try again.</Trans>}
              />
              <ResnapshotTrigger />
              <NormalizeTrigger title={t`Normalize platforms`} actionLabel={t`Run platform normalization`} mutationFn={normalizePlatforms} successMessage={normalizeSuccessMessage} />
              <NormalizeTrigger title={t`Normalize regions`} actionLabel={t`Run region normalization`} mutationFn={normalizeRegions} successMessage={normalizeSuccessMessage} />
              <NormalizeTrigger title={t`Normalize community regions`} actionLabel={t`Run community region normalization`} mutationFn={normalizeCommunityRegions} successMessage={normalizeSuccessMessage} />
            </div>
          </section>
        </div>
      ) : (
        <div role="tabpanel" id={SUBMISSIONS_PANEL} aria-labelledby={tabButtonId(SUBMISSIONS_PANEL)}>
          <SubmissionsQueue />
          <CommunityProducts />
        </div>
      )}
    </main>
  )
}
