import { useLingui } from '@lingui/react/macro'
import { Navigate } from 'react-router'
import { landingPageValues } from '../api/schema'
import { useDocumentTitle } from '../lib/useDocumentTitle'
import { useMe } from '../lib/useMe'

type LandingPage = (typeof landingPageValues)[number]

const targets: Record<LandingPage, string> = {
  collection: '/collection',
  feed: '/feed',
  explore: '/explore',
}

// Redirects to the user's landing_page preference. Layout already
// resolved ['me'] before mount, so this is a cache hit; renders
// nothing meanwhile, avoiding a loading flash.
export default function Home() {
  const { t } = useLingui()
  useDocumentTitle(t`Home`)
  const me = useMe()
  if (!me.data) return null
  return <Navigate to={targets[me.data.landing_page]} replace />
}
