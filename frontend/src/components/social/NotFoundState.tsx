import { Trans, useLingui } from '@lingui/react/macro'

// NotFoundState is the shared "nothing here" surface for social pages
// that resolve by a public identifier - a profile here, a shared
// shelf as well once that page lands. The 404 problem deliberately
// never distinguishes "does not exist" from "exists but private" (a
// probing request cannot tell them apart), so this component takes no
// props and renders identically no matter which one it was - the UI
// must not leak a distinction the API withholds on purpose.
export default function NotFoundState() {
  const { t } = useLingui()
  return (
    <main aria-label={t`Not found`} className="py-12 text-center text-gray-500" role="alert">
      <p><Trans>Nothing here.</Trans></p>
    </main>
  )
}
