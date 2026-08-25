import { Trans, useLingui } from '@lingui/react/macro'
import { Link } from 'react-router'
import { useDocumentTitle } from '../lib/useDocumentTitle'

export default function NotFound() {
  const { t } = useLingui()
  useDocumentTitle(t`Page not found`)
  return (
    <main id="main-content" tabIndex={-1} aria-label={t`Page not found`} className="p-6">
      <h2 className="mb-2 text-2xl font-bold">
        <Trans>Page not found</Trans>
      </h2>
      <p className="text-sm text-gray-600">
        <Trans>
          There is no page at this address.{' '}
          <Link to="/" className="underline hover:text-gray-900">
            Go to the start page
          </Link>
          .
        </Trans>
      </p>
    </main>
  )
}
