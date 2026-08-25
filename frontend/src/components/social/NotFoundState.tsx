import { Trans, useLingui } from '@lingui/react/macro'

// Takes no props and renders identically for "does not exist" vs "exists
// but private" - the UI must not leak a distinction the API withholds on
// purpose (a probing request can't tell them apart either).
export default function NotFoundState() {
  const { t } = useLingui()
  return (
    <main id="main-content" tabIndex={-1} aria-label={t`Not found`} className="py-12 text-center text-gray-500">
      <p role="alert"><Trans>Nothing here.</Trans></p>
    </main>
  )
}
