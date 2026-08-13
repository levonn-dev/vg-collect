import { Navigate } from 'react-router'
import type { Me } from '../api/client'
import { useMe } from '../lib/useMe'

const targets: Record<Me['landing_page'], string> = {
  collection: '/collection',
  feed: '/feed',
  explore: '/explore',
}

// Home is the bare "/" entry: it carries no content of its own, only a
// redirect to the signed-in user's landing_page preference. Layout has
// already resolved ['me'] before this ever mounts, so this read is a
// cache hit; render nothing while it is not yet available rather than
// flash a loading state under the redirect.
export default function Home() {
  const me = useMe()
  if (!me.data) return null
  return <Navigate to={targets[me.data.landing_page]} replace />
}
