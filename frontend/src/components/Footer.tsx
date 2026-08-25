import { Trans, useLingui } from '@lingui/react/macro'
import { Link } from 'react-router'
import { site } from '../lib/site'
import LocaleSwitch from './LocaleSwitch'

// Only mount point for LocaleSwitch, rendered in both shells so a signed-out
// visitor can still switch languages. No VITE_SITE_* values shows links only.
export default function Footer({ showHelp = false }: { showHelp?: boolean }) {
  const { t, i18n } = useLingui()
  const s = site()
  const { name, operator, contact } = s
  return (
    <footer
      aria-label={t`Site footer`}
      className="mt-8 border-t border-gray-200 pt-4 pb-2 text-xs text-gray-500"
    >
      <div className="flex items-start justify-between gap-4">
        {/* Beside the nav, not inside it: a control, not a page link. */}
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
        <LocaleSwitch />
      </div>
      {s.dataSources.length > 0 && (
        <ul className="mt-2 flex flex-wrap gap-x-4 gap-y-1">
          {s.dataSources.map((d) => {
            const dataType = i18n._(d.dataType)
            const label = d.label
            return (
              <li key={d.key}>
                <Trans>
                  {dataType} provided by{' '}
                  <a href={d.url} className="underline hover:text-gray-900">
                    {label}
                  </a>
                </Trans>
              </li>
            )
          })}
        </ul>
      )}
      {s.operator && (
        <p className="mt-2">
          {s.contact ? (
            <Trans>
              {name} is run by {operator}{' '}
              (
              <a href={`mailto:${s.contact}`} className="underline hover:text-gray-900">
                {contact}
              </a>
              )
            </Trans>
          ) : (
            <Trans>
              {name} is run by {operator}
            </Trans>
          )}
        </p>
      )}
    </footer>
  )
}
