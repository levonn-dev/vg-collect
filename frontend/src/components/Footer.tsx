import { Trans, useLingui } from '@lingui/react/macro'
import { Link } from 'react-router'
import { site } from '../lib/site'

// Footer renders on every page: page links, one credit line per data
// source this deployment runs, and the operator line when one is
// configured. A build with no VITE_SITE_* values shows links only.
export default function Footer({ showHelp = false }: { showHelp?: boolean }) {
  const { t, i18n } = useLingui()
  const s = site()
  return (
    <footer
      aria-label={t`Site footer`}
      className="mt-8 border-t border-gray-200 pt-4 pb-2 text-xs text-gray-500"
    >
      <nav aria-label={t`Site`} className="flex flex-wrap gap-x-4 gap-y-1">
        <Link to="/about" className="hover:text-gray-900">
          <Trans>About</Trans>
        </Link>
        <Link to="/terms" className="hover:text-gray-900">
          <Trans>Terms</Trans>
        </Link>
        <Link to="/privacy" className="hover:text-gray-900">
          <Trans>Privacy</Trans>
        </Link>
        {showHelp && (
          <Link to="/help" className="hover:text-gray-900">
            <Trans>Help</Trans>
          </Link>
        )}
        <a href={s.sourceUrl} className="hover:text-gray-900">
          <Trans>Source</Trans>
        </a>
      </nav>
      {s.dataSources.length > 0 && (
        <ul className="mt-2 flex flex-wrap gap-x-4 gap-y-1">
          {s.dataSources.map((d) => (
            <li key={d.key}>
              <Trans>
                {i18n._(d.dataType)} provided by{' '}
                <a href={d.url} className="underline hover:text-gray-900">
                  {d.label}
                </a>
              </Trans>
            </li>
          ))}
        </ul>
      )}
      {s.operator && (
        <p className="mt-2">
          {s.contact ? (
            <Trans>
              {s.name} is run by {s.operator}{' '}
              (
              <a href={`mailto:${s.contact}`} className="underline hover:text-gray-900">
                {s.contact}
              </a>
              )
            </Trans>
          ) : (
            <Trans>
              {s.name} is run by {s.operator}
            </Trans>
          )}
        </p>
      )}
    </footer>
  )
}
