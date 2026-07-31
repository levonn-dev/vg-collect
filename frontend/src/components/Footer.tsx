import { Link } from 'react-router'
import { site } from '../lib/site'

// Footer renders on every page: page links, one credit line per data
// source this deployment runs, and the operator line when one is
// configured. A build with no VITE_SITE_* values shows links only.
export default function Footer({ showHelp = false }: { showHelp?: boolean }) {
  const s = site()
  return (
    <footer
      aria-label="Site footer"
      className="mt-8 border-t border-gray-200 pt-4 pb-2 text-xs text-gray-500"
    >
      <nav aria-label="Site" className="flex flex-wrap gap-x-4 gap-y-1">
        <Link to="/about" className="hover:text-gray-900">
          About
        </Link>
        <Link to="/terms" className="hover:text-gray-900">
          Terms
        </Link>
        <Link to="/privacy" className="hover:text-gray-900">
          Privacy
        </Link>
        {showHelp && (
          <Link to="/help" className="hover:text-gray-900">
            Help
          </Link>
        )}
        <a href={s.sourceUrl} className="hover:text-gray-900">
          Source
        </a>
      </nav>
      {s.dataSources.length > 0 && (
        <ul className="mt-2 flex flex-wrap gap-x-4 gap-y-1">
          {s.dataSources.map((d) => (
            <li key={d.key}>
              {d.dataType} provided by{' '}
              <a href={d.url} className="underline hover:text-gray-900">
                {d.label}
              </a>
            </li>
          ))}
        </ul>
      )}
      {s.operator && (
        <p className="mt-2">
          {s.name} is run by {s.operator}
          {s.contact && (
            <>
              {' '}
              (
              <a href={`mailto:${s.contact}`} className="underline hover:text-gray-900">
                {s.contact}
              </a>
              )
            </>
          )}
        </p>
      )}
    </footer>
  )
}
